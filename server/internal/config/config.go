package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

type Config struct {
	Port      int             `json:"port"`
	DataDir   string          `json:"data_dir"`
	UploadDir string          `json:"upload_dir"`
	JWTSecret string          `json:"jwt_secret"`
	Dedup     DedupConfig     `json:"dedup"`
	Thumbnail ThumbnailConfig `json:"thumbnail"`
	HostRoot  string          `json:"-"` // 宿主机根目录在容器内的映射路径，通过环境变量 HOST_ROOT 设置
}

// DedupConfig 控制内容去重行为。
type DedupConfig struct {
	// Enabled 关闭后上传不做去重判定，存量扫描接口返回 503。
	Enabled bool `json:"enabled"`
	// SkipUploadDuplicate 上传命中已有内容时跳过写入（仅登记虚拟条目）。
	SkipUploadDuplicate bool `json:"skip_upload_duplicate"`
	// LargeFileThreshold 超过该大小先做部分哈希预筛。
	LargeFileThreshold int64 `json:"large_file_threshold"`
	// PartialHashBytes 部分哈希读取字节数。
	PartialHashBytes int64 `json:"partial_hash_bytes"`
	// MaxScanFiles 单次扫描文件数上限。
	MaxScanFiles int `json:"max_scan_files"`
	// HashWorkers 扫描阶段并发哈希协程数。
	HashWorkers int `json:"hash_workers"`
}

// ThumbnailConfig 控制缩略图生成与缓存。
type ThumbnailConfig struct {
	// MaxSize 缩略图长边上限。语义索引假定短边 >= 224，不要随意调小。
	MaxSize int `json:"max_size"`
	// JPEGQuality 缩略图编码质量。
	JPEGQuality int `json:"jpeg_quality"`
	// ValidateMtime 为 true 时，缓存比源文件旧即重新生成。
	// 关闭会让「同路径换内容」返回旧图，除非另有失效逻辑兜底。
	ValidateMtime bool `json:"validate_mtime"`
	// BackfillWorkers 批量补齐的并发数。实测 4 线程已接近 IO 瓶颈。
	BackfillWorkers int `json:"backfill_workers"`
	// PruneOrphans 补齐任务结束时顺带清理不再对应源文件的缓存。
	PruneOrphans bool `json:"prune_orphans"`
}

func (c *Config) ThumbCacheDir() string {
	return filepath.Join(c.DataDir, "thumb_cache")
}

// TrashDir 是清理动作的回收站目录。删除的冗余副本会按原虚拟路径存放，
// 以便误操作后恢复。
func (c *Config) TrashDir() string {
	return filepath.Join(c.DataDir, "trash")
}

func (c *Config) DBPath() string {
	return filepath.Join(c.DataDir, "db")
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		Port:    8080,
		DataDir: "./data",
		// 去重默认开启：布尔零值是 false，无法区分「未配置」，因此先在
		// 反序列化前置位，配置文件显式写 false 时才会被覆盖。
		Dedup: DedupConfig{
			Enabled:             true,
			SkipUploadDuplicate: true,
		},
		// 同理：ValidateMtime 的零值是 false，但正确行为是 true。
		// 先在反序列化前置位，配置文件显式写 false 才会被覆盖。
		Thumbnail: ThumbnailConfig{
			MaxSize:         320,
			JPEGQuality:     80,
			ValidateMtime:   true,
			BackfillWorkers: 4,
			PruneOrphans:    true,
		},
	}

	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}

	if cfg.UploadDir == "" {
		cfg.UploadDir = filepath.Join(cfg.DataDir, "files")
	}
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = generateRandomString(32)
		log.Println("Generated new JWT secret")
	}

	cfg.Dedup.applyDefaults()

	cfg.HostRoot = os.Getenv("HOST_ROOT")

	cfg.ensureDirs()
	return cfg, nil
}

// applyDefaults 补齐去重配置的缺省值。
//
// 布尔字段无法区分「未配置」与「显式关闭」，因此 enabled 的默认开启通过
// 「整段配置缺失」判断：配置文件里没有 dedup 字段时才启用。
func (d *DedupConfig) applyDefaults() {
	if d.LargeFileThreshold <= 0 {
		d.LargeFileThreshold = 50 * 1024 * 1024
	}
	if d.PartialHashBytes <= 0 {
		d.PartialHashBytes = 64 * 1024
	}
	if d.MaxScanFiles <= 0 {
		d.MaxScanFiles = 500000
	}
	if d.HashWorkers <= 0 {
		d.HashWorkers = 4
	}
}

func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (c *Config) ensureDirs() {
	dirs := []string{c.DataDir, c.UploadDir, c.ThumbCacheDir(), c.DBPath(), c.TrashDir()}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			log.Fatalf("Failed to create directory %s: %v", d, err)
		}
	}
}

func generateRandomString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
