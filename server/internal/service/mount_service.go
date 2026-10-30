package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zhangjh/mini-nas/server/internal/model"
)

type MountService struct {
	mu       sync.RWMutex
	mounts   []model.Mount
	filePath string
}

func NewMountService(dbDir string) *MountService {
	os.MkdirAll(dbDir, 0755)
	s := &MountService{
		mounts:   []model.Mount{},
		filePath: filepath.Join(dbDir, "mounts.json"),
	}
	s.load()
	return s
}

// AddDefaultMount 确保上传目录以一个「本地挂载」的形式存在。
//
// 文件读写全部走挂载点解析（ResolveMount），所以全新部署若挂载表为空，
// 任何上传都会因 "mount not found" 失败。这里在启动时兜底创建。
//
// 该挂载点标记 Local，因为 uploadDir 由 data_dir 派生、在容器内可直接访问，
// 不能再叠加 HOST_ROOT 前缀（那是给用户添加的宿主机路径用的）。
//
// 若已存在同路径或同名的挂载则不做任何事；返回实际生效的挂载点，供调用方打日志。
func (s *MountService) AddDefaultMount(uploadDir string) *model.Mount {
	abs, err := filepath.Abs(uploadDir)
	if err != nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i, m := range s.mounts {
		if m.Path == abs {
			return &s.mounts[i]
		}
	}

	name := filepath.Base(abs)
	for _, m := range s.mounts {
		if m.Name == name {
			// 名字被别的挂载占了，加后缀避免歧义
			name = name + "-upload"
			break
		}
	}

	mount := model.Mount{
		ID:        uuid.NewString(),
		Name:      name,
		Path:      abs,
		CreatedAt: time.Now().UnixMilli(),
		Local:     true,
	}
	s.mounts = append([]model.Mount{mount}, s.mounts...)
	s.save()
	return &s.mounts[0]
}

func (s *MountService) List() []model.Mount {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]model.Mount, len(s.mounts))
	copy(result, s.mounts)
	return result
}

func (s *MountService) Get(name string) *model.Mount {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, m := range s.mounts {
		if m.Name == name {
			return &m
		}
	}
	return nil
}

func (s *MountService) GetByPath(absPath string) *model.Mount {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, m := range s.mounts {
		if m.Path == absPath {
			return &m
		}
	}
	return nil
}

func (s *MountService) Add(name, path string) (*model.Mount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, m := range s.mounts {
		if m.Name == name {
			return nil, nil
		}
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	os.MkdirAll(abs, 0755)

	mount := model.Mount{
		ID:        uuid.NewString(),
		Name:      name,
		Path:      abs,
		CreatedAt: time.Now().UnixMilli(),
	}
	s.mounts = append(s.mounts, mount)
	s.save()
	return &mount, nil
}

func (s *MountService) Update(name, newPath string) (*model.Mount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, m := range s.mounts {
		if m.Name == name {
			abs, err := filepath.Abs(newPath)
			if err != nil {
				return nil, err
			}
			os.MkdirAll(abs, 0755)
			s.mounts[i].Path = abs
			s.save()
			return &s.mounts[i], nil
		}
	}
	return nil, nil
}

func (s *MountService) Delete(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, m := range s.mounts {
		if m.Name == name {
			s.mounts = append(s.mounts[:i], s.mounts[i+1:]...)
			s.save()
			return true
		}
	}
	return false
}

func (s *MountService) load() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return
	}
	json.Unmarshal(data, &s.mounts)
}

func (s *MountService) save() {
	data, _ := json.MarshalIndent(s.mounts, "", "  ")
	os.WriteFile(s.filePath, data, 0644)
}

// resolveMount extracts the mount name and relative path from a virtual path.
// e.g. "照片/DSC001.jpg" → mount="照片", rel="DSC001.jpg"
// e.g. "" → mount="", rel=""
func (s *MountService) ResolveMount(virtualPath string) (*model.Mount, string, string) {
	if virtualPath == "" || virtualPath == "." {
		return nil, "", ""
	}

	parts := splitPath(virtualPath)
	mountName := parts[0]
	var rel string
	if len(parts) > 1 {
		rel = filepath.Join(parts[1:]...)
	}

	mount := s.Get(mountName)
	if mount == nil {
		return nil, mountName, rel
	}
	return mount, mountName, rel
}

func splitPath(p string) []string {
	clean := filepath.ToSlash(filepath.Clean(p))
	clean = strings.Trim(clean, "/")
	if clean == "" || clean == "." {
		return nil
	}
	return strings.Split(clean, "/")
}
