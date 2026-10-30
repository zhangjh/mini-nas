package service

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/disintegration/imaging"
	"github.com/zhangjh/mini-nas/server/internal/imgcodec"
)

// ThumbnailService 负责生成与缓存图片缩略图。
//
// 缓存键取源路径的 SHA-1 前 20 位，而不是把路径里的分隔符替换成下划线。
// 后者有两个隐患：a/b.jpg 与 a_b.jpg 会映射到同一个键；深层路径扁平化后
// 可能超过文件名长度上限。
type ThumbnailService struct {
	CacheDir string
	MaxSize  int
	Quality  int
	// ValidateMtime 为 true 时，缓存文件比源文件旧即视为失效。
	// 关闭后只判断文件是否存在——同路径换内容会拿到旧图。
	ValidateMtime bool
}

func NewThumbnailService(cacheDir string) *ThumbnailService {
	os.MkdirAll(cacheDir, 0o755)
	return &ThumbnailService{
		CacheDir:      cacheDir,
		MaxSize:       320,
		Quality:       80,
		ValidateMtime: true,
	}
}

// CachePath 返回源文件对应的缓存路径。源路径应使用进程内可直接访问的绝对路径。
func (s *ThumbnailService) CachePath(srcPath string) string {
	sum := sha1.Sum([]byte(filepath.ToSlash(srcPath)))
	return filepath.Join(s.CacheDir, hex.EncodeToString(sum[:])[:20]+".jpg")
}

// Get 返回缩略图路径；缓存缺失或已过期时同步生成。
//
// 源文件不存在、扩展名不支持时返回空路径与 nil error——调用方据此返回 404，
// 而不是当作内部错误。
func (s *ThumbnailService) Get(srcPath string) (string, error) {
	if srcPath == "" {
		return "", nil
	}
	dst := s.CachePath(srcPath)
	if s.fresh(srcPath, dst) {
		return dst, nil
	}
	return s.generate(srcPath)
}

// fresh 判断缓存是否可直接使用。
func (s *ThumbnailService) fresh(srcPath, dst string) bool {
	srcInfo, err := os.Stat(srcPath)
	if err != nil || srcInfo.IsDir() {
		return false
	}
	dstInfo, err := os.Stat(dst)
	if err != nil {
		return false
	}
	if !s.ValidateMtime {
		return true
	}
	// 缓存一定在源文件之后写入，正常情况下 dst 更新。
	// 允许相等，避免同一秒内写入时反复重建。
	return !dstInfo.ModTime().Before(srcInfo.ModTime())
}

// generate 解码源文件并写入缓存，返回缓存路径。
func (s *ThumbnailService) generate(srcPath string) (string, error) {
	if !imgcodec.Supports(srcPath) {
		return "", nil
	}

	img, err := imgcodec.DecodeFile(srcPath, s.MaxSize)
	if err != nil {
		return "", err
	}

	thumb := s.fit(img)
	dst := s.CachePath(srcPath)
	if err := s.writeJPEG(thumb, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// fit 等比缩放到 MaxSize 见方的框内；已经足够小的图不放大。
func (s *ThumbnailService) fit(img image.Image) image.Image {
	b := img.Bounds()
	if b.Dx() <= s.MaxSize && b.Dy() <= s.MaxSize {
		return img
	}
	return imaging.Fit(img, s.MaxSize, s.MaxSize, imaging.Lanczos)
}

// writeJPEG 原子写入：先写临时文件再 rename，避免并发读到半截文件。
func (s *ThumbnailService) writeJPEG(img image.Image, dst string) error {
	tmp := dst + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := imaging.Encode(f, img, imaging.JPEG, imaging.JPEGQuality(s.Quality)); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Invalidate 删除某个源文件对应的缓存。
//
// 删除、改名、移动、覆盖上传之后都必须调用，否则缓存会继续指向旧内容。
func (s *ThumbnailService) Invalidate(srcPath string) {
	if srcPath == "" {
		return
	}
	os.Remove(s.CachePath(srcPath))
}

// InvalidateMany 批量失效，忽略不存在的条目。
func (s *ThumbnailService) InvalidateMany(srcPaths ...string) {
	for _, p := range srcPaths {
		s.Invalidate(p)
	}
}

// BackfillStats 汇总一次批量补齐的结果。
type BackfillStats struct {
	Total       int // 待检查的源文件数
	Generated   int // 实际生成的缩略图数
	Skipped     int // 缓存已新鲜的跳过数
	Failed      int // 解码或写盘失败数
	Unsupported int // 扩展名不支持
}

// Backfill 为一批源文件补齐缩略图。
//
// force 为 true 时忽略缓存是否新鲜，全部重建。progress 可为 nil，
// 每完成一个文件回调一次，由调用方决定上报频率。
func (s *ThumbnailService) Backfill(
	ctx context.Context,
	srcPaths []string,
	workers int,
	force bool,
	progress func(done, total int),
) BackfillStats {
	stats := BackfillStats{Total: len(srcPaths)}
	if workers <= 0 {
		workers = 4
	}

	var done, generated, skipped, failed, unsupported atomic.Int64
	jobs := make(chan string)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for src := range jobs {
				if ctx.Err() != nil {
					continue // 排空 channel，避免生产者阻塞
				}
				switch s.fillOne(src, force) {
				case fillGenerated:
					generated.Add(1)
				case fillSkipped:
					skipped.Add(1)
				case fillUnsupported:
					unsupported.Add(1)
				case fillFailed:
					failed.Add(1)
				}
				n := done.Add(1)
				if progress != nil {
					progress(int(n), len(srcPaths))
				}
			}
		}()
	}

	for _, p := range srcPaths {
		if ctx.Err() != nil {
			break
		}
		jobs <- p
	}
	close(jobs)
	wg.Wait()

	stats.Generated = int(generated.Load())
	stats.Skipped = int(skipped.Load())
	stats.Failed = int(failed.Load())
	stats.Unsupported = int(unsupported.Load())
	return stats
}

type fillResult int

const (
	fillSkipped fillResult = iota
	fillGenerated
	fillFailed
	fillUnsupported
)

func (s *ThumbnailService) fillOne(srcPath string, force bool) fillResult {
	if !imgcodec.Supports(srcPath) {
		return fillUnsupported
	}
	dst := s.CachePath(srcPath)
	if !force && s.fresh(srcPath, dst) {
		return fillSkipped
	}
	if _, err := s.generate(srcPath); err != nil {
		return fillFailed
	}
	return fillGenerated
}

// PruneOrphans 删除不再对应任何源文件的缓存，返回删除数量与释放字节数。
//
// keep 是需要保留的缓存文件名集合，由调用方遍历源文件后构造。
func (s *ThumbnailService) PruneOrphans(keep map[string]struct{}) (int, int64, error) {
	entries, err := os.ReadDir(s.CacheDir)
	if err != nil {
		return 0, 0, err
	}
	removed := 0
	var freed int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ok := keep[e.Name()]; ok {
			continue
		}
		path := filepath.Join(s.CacheDir, e.Name())
		// 先取大小再删；Info 失败就当作 0 字节，不影响清理。
		var size int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		if err := os.Remove(path); err == nil {
			removed++
			freed += size
		}
	}
	return removed, freed, nil
}

// Ensure 强制重建并返回缓存路径，用于调试与自检。
func (s *ThumbnailService) Ensure(srcPath string) (string, error) {
	if srcPath == "" {
		return "", fmt.Errorf("empty path")
	}
	return s.generate(srcPath)
}
