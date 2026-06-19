package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zhangjh/mini-nas/server/internal/model"
)

type FileService struct {
	mountSvc *MountService
}

func NewFileService(mountSvc *MountService) *FileService {
	return &FileService{mountSvc: mountSvc}
}

func (s *FileService) AbsPath(virtualPath string) string {
	mount, _, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return ""
	}
	return filepath.Join(mount.Path, rel)
}

func (s *FileService) ListDir(virtualPath string, page, pageSize int, sortBy string, sortDesc bool) (*model.FileList, error) {
	if virtualPath == "" || virtualPath == "." {
		return s.listMounts(page, pageSize)
	}

	mount, mountName, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return nil, os.ErrNotExist
	}

	absDir := filepath.Join(mount.Path, rel)
	return s.listDir(absDir, mountName, rel, page, pageSize, sortBy, sortDesc)
}

func (s *FileService) listMounts(page, pageSize int) (*model.FileList, error) {
	mounts := s.mountSvc.List()
	var items []model.FileInfo
	for _, m := range mounts {
		info, err := os.Stat(m.Path)
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
		virtualPath := filepath.Join(mountName, rel, entry.Name())
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

	virtualBase := filepath.Join(mountName, rel)
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
		return nil, os.ErrNotExist
	}
	abs := filepath.Join(mount.Path, rel)
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

func (s *FileService) Mkdir(virtualPath string) error {
	mount, _, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return fmt.Errorf("mount not found")
	}
	return os.MkdirAll(filepath.Join(mount.Path, rel), 0755)
}

func (s *FileService) Delete(virtualPath string) error {
	mount, _, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return fmt.Errorf("mount not found")
	}
	return os.RemoveAll(filepath.Join(mount.Path, rel))
}

func (s *FileService) Rename(oldPath, newPath string) error {
	oldMount, _, oldRel := s.mountSvc.ResolveMount(oldPath)
	newMount, _, newRel := s.mountSvc.ResolveMount(newPath)
	if oldMount == nil || newMount == nil {
		return fmt.Errorf("mount not found")
	}

	oldAbs := filepath.Join(oldMount.Path, oldRel)
	newAbs := filepath.Join(newMount.Path, newRel)
	dir := filepath.Dir(newAbs)
	os.MkdirAll(dir, 0755)

	return os.Rename(oldAbs, newAbs)
}

func (s *FileService) Move(paths []string, destDir string) error {
	destMount, _, destRel := s.mountSvc.ResolveMount(destDir)
	if destMount == nil {
		return fmt.Errorf("destination mount not found")
	}
	destAbs := filepath.Join(destMount.Path, destRel)
	os.MkdirAll(destAbs, 0755)

	for _, p := range paths {
		mount, _, rel := s.mountSvc.ResolveMount(p)
		if mount == nil {
			continue
		}
		src := filepath.Join(mount.Path, rel)
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
		return fmt.Errorf("destination mount not found")
	}
	destAbs := filepath.Join(destMount.Path, destRel)
	os.MkdirAll(destAbs, 0755)

	for _, p := range srcPaths {
		mount, _, rel := s.mountSvc.ResolveMount(p)
		if mount == nil {
			continue
		}
		src := filepath.Join(mount.Path, rel)
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
		return 0, fmt.Errorf("mount not found")
	}

	abs := filepath.Join(mount.Path, rel)
	dir := filepath.Dir(abs)
	os.MkdirAll(dir, 0755)

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

func (s *FileService) OpenFile(virtualPath string) (*os.File, error) {
	mount, _, rel := s.mountSvc.ResolveMount(virtualPath)
	if mount == nil {
		return nil, fmt.Errorf("mount not found")
	}
	return os.Open(filepath.Join(mount.Path, rel))
}

func (s *FileService) Search(q string, virtualPath string) []model.FileInfo {
	mount, _, rel := s.mountSvc.ResolveMount(virtualPath)

	var rootDir string
	if mount != nil {
		rootDir = filepath.Join(mount.Path, rel)
	} else {
		rootDir = "/"
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
			relPath, err := filepath.Rel(m.Path, p)
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
