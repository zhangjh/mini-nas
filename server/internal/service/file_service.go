package service

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zhangjh/mini-nas/server/internal/model"
)

type fileLock struct {
	mu sync.RWMutex
}

// ErrMountNotFound 表示虚拟路径的第一段不是已注册的挂载点。
//
// 这是**调用方给错了路径**，不是服务端故障，API 层应映射成 400 而不是 500。
// 典型场景：客户端在根视图（此时没有选中任何挂载）直接上传或建目录，
// 传过来的路径不带挂载名前缀。
var ErrMountNotFound = errors.New("mount not found")

// virtualJoin 拼接虚拟路径。
//
// 虚拟路径是 API 契约的一部分，统一使用 "/"：客户端（Android/Web）传的是
// 正斜杠，容器内 filepath.Join 也是正斜杠，只有 Windows 开发环境会给出反斜杠。
// 用 path.Join 而非 filepath.Join 可让三种环境行为一致。
func virtualJoin(elem ...string) string {
	parts := make([]string, len(elem))
	for i, e := range elem {
		// filepath.Rel 在 Windows 下返回反斜杠，先归一化再拼接。
		parts[i] = filepath.ToSlash(e)
	}
	return path.Join(parts...)
}

// SkipIndex 提供「内容已去重、未落盘」的虚拟条目，用于补齐目录树视图。
//
// 去重跳过是服务端行为，文件系统上并不存在对应文件；如果 ListTree 不把它们
// 补进来，同步客户端在 manifest 为空时会把这些文件重传一遍。
type SkipIndex interface {
	ListSkips(prefix string) ([]model.DedupSkip, error)
	VerifySkipTarget(skip *model.DedupSkip) bool
}

type FileService struct {
	mountSvc  *MountService
	hostRoot  string // 宿主机根目录在容器内的映射路径，为空则直接用原始路径
	fileLocks sync.Map
	skipIndex SkipIndex
}

func NewFileService(mountSvc *MountService, hostRoot string) *FileService {
	return &FileService{mountSvc: mountSvc, hostRoot: hostRoot}
}

// SetSkipIndex 注入去重跳过记录的查询能力。未注入时 ListTree 只返回真实文件。
func (s *FileService) SetSkipIndex(idx SkipIndex) {
	s.skipIndex = idx
}

// hostPath 将宿主机路径映射为容器内实际路径
func (s *FileService) hostPath(path string) string {
	if s.hostRoot == "" {
		return path
	}
	return filepath.Join(s.hostRoot, path)
}

// mountPath 把挂载点下的相对路径解析为进程内可直接访问的绝对路径。
//
// 用户添加的挂载点指向宿主机路径，需叠加 HOST_ROOT；而本地挂载（默认上传目录）
// 本身就挂在容器内，叠加会得到 /host/data/files 这种不存在的路径。
func (s *FileService) mountPath(m *model.Mount, rel string) string {
	p := filepath.Join(m.Path, rel)
	if m.Local {
		return p
	}
	return s.hostPath(p)
}

// lockKey 返回用于文件锁的绝对路径键
func (s *FileService) lockKey(virtualPath string) string {
	mount, _, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return virtualPath
	}
	return s.mountPath(mount, rel)
}

func (s *FileService) RLockFile(virtualPath string) {
	key := s.lockKey(virtualPath)
	v, _ := s.fileLocks.LoadOrStore(key, &fileLock{})
	v.(*fileLock).mu.RLock()
}

func (s *FileService) RUnlockFile(virtualPath string) {
	key := s.lockKey(virtualPath)
	v, ok := s.fileLocks.Load(key)
	if !ok {
		return
	}
	v.(*fileLock).mu.RUnlock()
}

func (s *FileService) LockFile(virtualPath string) {
	key := s.lockKey(virtualPath)
	v, _ := s.fileLocks.LoadOrStore(key, &fileLock{})
	v.(*fileLock).mu.Lock()
}

func (s *FileService) UnlockFile(virtualPath string) {
	key := s.lockKey(virtualPath)
	v, ok := s.fileLocks.Load(key)
	if !ok {
		return
	}
	v.(*fileLock).mu.Unlock()
}

func (s *FileService) AbsPath(virtualPath string) string {
	mount, _, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return ""
	}
	return s.mountPath(mount, rel)
}

func (s *FileService) ListDir(virtualPath string, page, pageSize int, sortBy string, sortDesc bool) (*model.FileList, error) {
	if virtualPath == "" || virtualPath == "." {
		return s.listMounts(page, pageSize)
	}

	mount, mountName, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return nil, ErrMountNotFound
	}

	absDir := s.mountPath(mount, rel)
	return s.listDir(absDir, mountName, rel, page, pageSize, sortBy, sortDesc)
}

func (s *FileService) listMounts(page, pageSize int) (*model.FileList, error) {
	mounts := s.mountSvc.List()
	var items []model.FileInfo
	for _, m := range mounts {
		info, err := os.Stat(s.mountPath(&m, ""))
		if err != nil {
			continue
		}
		items = append(items, model.FileInfo{
			Name:    m.Name,
			Path:    m.Name,
			Size:    info.Size(),
			IsDir:   true,
			ModTime: info.ModTime().UnixMilli(),
			Mode:    info.Mode() | os.ModeDir,
		})
	}

	total := len(items)
	if pageSize > 0 {
		start := (page - 1) * pageSize
		if start > total {
			start = total
		}
		end := start + pageSize
		if end > total {
			end = total
		}
		items = items[start:end]
	}

	return &model.FileList{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Path:     "",
	}, nil
}

func (s *FileService) listDir(absDir, mountName, rel string, page, pageSize int, sortBy string, sortDesc bool) (*model.FileList, error) {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}

	var files []model.FileInfo
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		virtualPath := virtualJoin(mountName, rel, entry.Name())
		files = append(files, model.FileInfo{
			Name:    entry.Name(),
			Path:    virtualPath,
			Size:    info.Size(),
			IsDir:   entry.IsDir(),
			ModTime: info.ModTime().UnixMilli(),
			Mode:    info.Mode(),
		})
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].IsDir != files[j].IsDir {
			return files[i].IsDir
		}
		var less bool
		switch sortBy {
		case "size":
			less = files[i].Size < files[j].Size
		case "time":
			less = files[i].ModTime < files[j].ModTime
		default:
			less = strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
		}
		if sortDesc {
			return !less
		}
		return less
	})

	total := len(files)
	if pageSize > 0 {
		start := (page - 1) * pageSize
		if start > total {
			start = total
		}
		end := start + pageSize
		if end > total {
			end = total
		}
		files = files[start:end]
	}

	virtualBase := virtualJoin(mountName, rel)
	return &model.FileList{
		Items:    files,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Path:     virtualBase,
	}, nil
}

func (s *FileService) GetFileInfo(virtualPath string) (*model.FileInfo, error) {
	if virtualPath == "" || virtualPath == "." {
		return &model.FileInfo{
			Name:  "根目录",
			Path:  "",
			IsDir: true,
		}, nil
	}

	mount, _, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return nil, ErrMountNotFound
	}
	abs := s.mountPath(mount, rel)
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}

	return &model.FileInfo{
		Name:    info.Name(),
		Path:    virtualPath,
		Size:    info.Size(),
		IsDir:   info.IsDir(),
		ModTime: info.ModTime().UnixMilli(),
		Mode:    info.Mode(),
	}, nil
}

// ListTree recursively walks the directory at virtualPath and returns all
// files (not directories) with their virtual paths and sizes. Used by the
// Android sync client to skip files that already exist on the server when
// the local manifest is empty (e.g., first sync, or task recreated).
//
// 内容去重命中时服务端不落盘，磁盘上并没有对应文件，因此这里会把
// dedup_skip 中的虚拟条目一并返回（携带原始大小）。客户端现有的
// 「路径 + 大小一致则跳过」逻辑无需改动即可避免重传。
//
// If the directory does not exist yet, an empty slice is returned (nothing
// has been synced yet).
func (s *FileService) ListTree(virtualPath string) ([]model.FileInfo, error) {
	mount, mountName, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return nil, ErrMountNotFound
	}

	absDir := s.mountPath(mount, rel)

	// seen 只用于给下面的 dedup_skip 条目去重。未注入 skipIndex 时没人读它，
	// 而大库下单是这张 map 就要几十万次插入 + 几十 MB 内存，故按需分配。
	var seen map[string]struct{}
	if s.skipIndex != nil {
		seen = make(map[string]struct{})
	}

	var files []model.FileInfo

	if _, err := os.Stat(absDir); err == nil {
		// WalkDir 直接复用 ReadDir 拿到的类型信息，目录条目不再额外 Lstat。
		// 文件仍需一次 Info() 取大小与 mtime（getdents 不返回这些），
		// 所以收益主要在目录密集的树上，不是数量级的改善。
		walkErr := filepath.WalkDir(absDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable entries
			}
			if d.IsDir() {
				return nil
			}
			relToWalk, err := filepath.Rel(absDir, p)
			if err != nil {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil // 遍历期间被删除，跳过即可
			}
			virtualFile := virtualJoin(mountName, rel, relToWalk)
			if seen != nil {
				seen[virtualFile] = struct{}{}
			}
			files = append(files, model.FileInfo{
				Name:    info.Name(),
				Path:    virtualFile,
				Size:    info.Size(),
				IsDir:   false,
				ModTime: info.ModTime().UnixMilli(),
				Mode:    info.Mode(),
			})
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// 补入去重跳过的虚拟条目。
	if s.skipIndex != nil {
		base := virtualJoin(mountName, rel)
		skips, err := s.skipIndex.ListSkips(base)
		if err == nil {
			for i := range skips {
				skip := &skips[i]
				if _, ok := seen[skip.Path]; ok {
					continue
				}
				// 原件已消失的跳过记录不可再用，跳过以免误导客户端。
				if !s.skipIndex.VerifySkipTarget(skip) {
					continue
				}
				files = append(files, model.FileInfo{
					Name:    filepath.Base(skip.Path),
					Path:    skip.Path,
					Size:    skip.Size,
					IsDir:   false,
					ModTime: skip.MTime,
					Mode:    0644,
				})
			}
		}
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func (s *FileService) Mkdir(virtualPath string) error {
	mount, _, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return ErrMountNotFound
	}
	return os.MkdirAll(s.mountPath(mount, rel), 0755)
}

func (s *FileService) Delete(virtualPath string) error {
	mount, _, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return ErrMountNotFound
	}
	return os.RemoveAll(s.mountPath(mount, rel))
}

func (s *FileService) Rename(oldPath, newPath string) error {
	oldMount, _, oldRel := s.mountSvc.ResolveMount(oldPath)
	newMount, _, newRel := s.mountSvc.ResolveMount(newPath)
	if oldMount == nil || newMount == nil {
		return ErrMountNotFound
	}

	oldAbs := s.mountPath(oldMount, oldRel)
	newAbs := s.mountPath(newMount, newRel)
	dir := filepath.Dir(newAbs)
	os.MkdirAll(dir, 0755)

	return os.Rename(oldAbs, newAbs)
}

func (s *FileService) Move(paths []string, destDir string) error {
	destMount, _, destRel := s.mountSvc.ResolveMount(destDir)
	if destMount == nil {
		return fmt.Errorf("destination %w", ErrMountNotFound)
	}
	destAbs := s.mountPath(destMount, destRel)
	os.MkdirAll(destAbs, 0755)

	for _, p := range paths {
		mount, _, rel := s.mountSvc.ResolveMount(p)
		if mount == nil {
			continue
		}
		src := s.mountPath(mount, rel)
		dst := filepath.Join(destAbs, filepath.Base(rel))
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileService) Copy(srcPaths []string, destDir string) error {
	destMount, _, destRel := s.mountSvc.ResolveMount(destDir)
	if destMount == nil {
		return fmt.Errorf("destination %w", ErrMountNotFound)
	}
	destAbs := s.mountPath(destMount, destRel)
	os.MkdirAll(destAbs, 0755)

	for _, p := range srcPaths {
		mount, _, rel := s.mountSvc.ResolveMount(p)
		if mount == nil {
			continue
		}
		src := s.mountPath(mount, rel)
		dst := filepath.Join(destAbs, filepath.Base(rel))
		if err := copyPath(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileService) WriteFile(virtualPath string, reader io.Reader) (int64, error) {
	mount, _, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return 0, ErrMountNotFound
	}

	abs := s.mountPath(mount, rel)
	dir := filepath.Dir(abs)
	os.MkdirAll(dir, 0755)

	// 写锁：防止并发读取读到不完整文件
	s.LockFile(virtualPath)
	defer s.UnlockFile(virtualPath)

	tmpPath := fmt.Sprintf("%s.tmp.%d", abs, time.Now().UnixNano())
	f, err := os.Create(tmpPath)
	if err != nil {
		return 0, err
	}

	written, err := io.Copy(f, reader)
	if err != nil {
		f.Close()
		os.Remove(tmpPath)
		return 0, err
	}
	f.Close()

	if err := os.Rename(tmpPath, abs); err != nil {
		os.Remove(tmpPath)
		return 0, err
	}
	return written, nil
}

// PendingWrite 是一次已落盘到临时文件、但尚未提交到目标路径的上传。
//
// 引入它的目的是让上传路径可以在「写完整个文件」之后再决定是否落盘：
// 内容去重需要完整 MD5，而 MD5 只有读完全部字节才能算出。把「暂存」与
// 「提交」拆开，就能在不重复读流的前提下做去重判定。
type PendingWrite struct {
	svc         *FileService
	virtualPath string
	absPath     string
	tmpPath     string
	MD5         string
	Size        int64
}

// StageFile 把 reader 的内容写入目标路径同目录下的临时文件，并计算 MD5。
func (s *FileService) StageFile(virtualPath string, reader io.Reader) (*PendingWrite, error) {
	mount, _, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return nil, ErrMountNotFound
	}

	abs := s.mountPath(mount, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return nil, err
	}

	tmpPath := fmt.Sprintf("%s.tmp.%d", abs, time.Now().UnixNano())
	f, err := os.Create(tmpPath)
	if err != nil {
		return nil, err
	}

	digest := md5.New()
	written, copyErr := io.Copy(io.MultiWriter(f, digest), reader)
	syncErr := f.Sync()
	closeErr := f.Close()

	if copyErr != nil {
		os.Remove(tmpPath)
		return nil, copyErr
	}
	if syncErr != nil {
		os.Remove(tmpPath)
		return nil, syncErr
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return nil, closeErr
	}

	return &PendingWrite{
		svc:         s,
		virtualPath: virtualPath,
		absPath:     abs,
		tmpPath:     tmpPath,
		MD5:         hex.EncodeToString(digest.Sum(nil)),
		Size:        written,
	}, nil
}

// Commit 把临时文件原子地移动到目标路径。
func (p *PendingWrite) Commit() error {
	p.svc.LockFile(p.virtualPath)
	defer p.svc.UnlockFile(p.virtualPath)

	if err := os.Rename(p.tmpPath, p.absPath); err != nil {
		os.Remove(p.tmpPath)
		return err
	}
	p.tmpPath = ""
	return nil
}

// Discard 丢弃暂存内容。已提交或已丢弃时是空操作。
func (p *PendingWrite) Discard() {
	if p.tmpPath == "" {
		return
	}
	os.Remove(p.tmpPath)
	p.tmpPath = ""
}

func (s *FileService) OpenFile(virtualPath string) (*os.File, error) {
	mount, _, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return nil, ErrMountNotFound
	}
	return os.Open(s.mountPath(mount, rel))
}

// Exists 判断虚拟路径是否对应一个已存在的普通文件。
func (s *FileService) Exists(virtualPath string) bool {
	mount, _, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return false
	}
	info, err := os.Stat(s.mountPath(mount, rel))
	return err == nil && !info.IsDir()
}

func (s *FileService) Search(q string, virtualPath string) []model.FileInfo {
	mount, _, rel := s.mountSvc.ResolveMount(virtualPath)

	var rootDir string
	if mount != nil {
		rootDir = s.mountPath(mount, rel)
	} else {
		rootDir = s.hostPath("/")
	}

	var results []model.FileInfo
	filepath.Walk(rootDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !strings.Contains(strings.ToLower(info.Name()), strings.ToLower(q)) {
			return nil
		}

		// Build virtual path: find which mount this file belongs to
		for _, m := range s.mountSvc.List() {
			mPath := s.mountPath(&m, "")
			relPath, err := filepath.Rel(mPath, p)
			if err == nil && !strings.HasPrefix(relPath, "..") {
				vp := filepath.Join(m.Name, relPath)
				results = append(results, model.FileInfo{
					Name:    info.Name(),
					Path:    vp,
					Size:    info.Size(),
					IsDir:   info.IsDir(),
					ModTime: info.ModTime().UnixMilli(),
					Mode:    info.Mode(),
				})
				break
			}
		}

		if len(results) >= 100 {
			return filepath.SkipAll
		}
		return nil
	})

	return results
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, path)
			target := filepath.Join(dst, rel)
			if info.IsDir() {
				return os.MkdirAll(target, 0755)
			}
			return copyFile(path, target)
		})
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()

	w, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer w.Close()

	_, err = io.Copy(w, r)
	return err
}
