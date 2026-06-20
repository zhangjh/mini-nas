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
	Port      int    `json:"port"`
	DataDir   string `json:"data_dir"`
	UploadDir string `json:"upload_dir"`
	JWTSecret string `json:"jwt_secret"`
}

func (c *Config) ThumbCacheDir() string {
	return filepath.Join(c.DataDir, "thumb_cache")
}

func (c *Config) DBPath() string {
	return filepath.Join(c.DataDir, "db")
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		Port:    8080,
		DataDir: "./data",
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

	cfg.ensureDirs()
	return cfg, nil
}

func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (c *Config) ensureDirs() {
	dirs := []string{c.DataDir, c.UploadDir, c.ThumbCacheDir(), c.DBPath()}
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
