package service

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/disintegration/imaging"
)

type ThumbnailService struct {
	CacheDir string
	MaxSize  int
}

func NewThumbnailService(cacheDir string) *ThumbnailService {
	os.MkdirAll(cacheDir, 0755)
	return &ThumbnailService{
		CacheDir: cacheDir,
		MaxSize:  320,
	}
}

func (s *ThumbnailService) GetThumbnail(filePath string) (string, error) {
	cachePath := s.cachePath(filePath)
	if _, err := os.Stat(cachePath); err == nil {
		return cachePath, nil
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp":
		return s.generateImageThumb(filePath, cachePath)
	default:
		return "", nil
	}
}

func (s *ThumbnailService) cachePath(filePath string) string {
	name := strings.ReplaceAll(filePath, string(filepath.Separator), "_")
	return filepath.Join(s.CacheDir, name+".jpg")
}

func (s *ThumbnailService) generateImageThumb(srcPath, cachePath string) (string, error) {
	src, err := imaging.Open(srcPath)
	if err != nil {
		return "", err
	}

	thumb := imaging.Fit(src, s.MaxSize, s.MaxSize, imaging.Lanczos)
	if err := imaging.Save(thumb, cachePath, imaging.JPEGQuality(80)); err != nil {
		return "", err
	}
	return cachePath, nil
}
