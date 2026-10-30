package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/zhangjh/mini-nas/server/internal/api"
	"github.com/zhangjh/mini-nas/server/internal/config"
	"github.com/zhangjh/mini-nas/server/internal/service"
	"github.com/zhangjh/mini-nas/server/internal/store"
)

func main() {
	configPath := flag.String("config", "config.json", "path to configuration file")
	port := flag.Int("port", 0, "port to listen on")
	dataDir := flag.String("data", "", "data directory path")
	flag.Parse()

	absConfigPath := *configPath
	if !filepath.IsAbs(absConfigPath) {
		wd, _ := os.Getwd()
		absConfigPath = filepath.Join(wd, absConfigPath)
	}

	cfg, err := config.Load(absConfigPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if *port > 0 {
		cfg.Port = *port
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
		cfg.UploadDir = filepath.Join(cfg.DataDir, "files")
	}

	if err := cfg.Save(absConfigPath); err != nil {
		log.Printf("Warning: could not save config: %v", err)
	}

	mountSvc := service.NewMountService(cfg.DBPath())
	userSvc := service.NewUserService(cfg.DBPath())

	// 全新部署的挂载表是空的，而所有文件读写都要先经挂载点解析，
	// 不兜底的话首次上传必然 mount not found。
	if m := mountSvc.AddDefaultMount(cfg.UploadDir); m != nil {
		log.Printf("Default upload mount: %s -> %s (local)", m.Name, m.Path)
	}

	fileSvc := service.NewFileService(mountSvc, cfg.HostRoot)
	thumbSvc := service.NewThumbnailService(cfg.ThumbCacheDir())
	thumbSvc.MaxSize = cfg.Thumbnail.MaxSize
	thumbSvc.Quality = cfg.Thumbnail.JPEGQuality
	thumbSvc.ValidateMtime = cfg.Thumbnail.ValidateMtime

	db, err := store.Open(cfg.DBPath())
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// 后台任务不跨进程恢复，重启后把残留的 RUNNING 任务标记为失败，
	// 否则前端会一直看到「运行中」。
	if n, err := db.FailStaleJobs("服务重启，任务已中断"); err != nil {
		log.Printf("Warning: could not clean stale jobs: %v", err)
	} else if n > 0 {
		log.Printf("Marked %d stale job(s) as failed", n)
	}

	hashSvc := service.NewHashService(db, fileSvc.AbsPath)
	fileSvc.SetSkipIndex(hashSvc)

	dedupOpts := service.DedupOptions{
		LargeFileThreshold: cfg.Dedup.LargeFileThreshold,
		PartialHashBytes:   cfg.Dedup.PartialHashBytes,
		MaxScanFiles:       cfg.Dedup.MaxScanFiles,
		HashWorkers:        cfg.Dedup.HashWorkers,
		ExcludePaths:       excludedPaths(cfg),
	}
	dedupSvc := service.NewDedupService(db, mountSvc, hashSvc, dedupOpts, cfg.TrashDir())

	thumbJobs := service.NewThumbJobService(
		db, mountSvc, thumbSvc, fileSvc,
		cfg.Thumbnail.BackfillWorkers, cfg.Thumbnail.PruneOrphans,
	)

	apiRouter := api.NewRouter(cfg, fileSvc, thumbSvc, mountSvc, userSvc, hashSvc, dedupSvc, thumbJobs)
	staticSrv := spaFallback(staticHandler())

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {
			apiRouter.ServeHTTP(w, r)
		} else {
			staticSrv.ServeHTTP(w, r)
		}
	})

	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("mini-nas server starting on %s", addr)
		log.Printf("Data directory: %s", cfg.DataDir)
		log.Printf("Upload directory: %s", cfg.UploadDir)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Println("Server stopped")
}

// excludedPaths 返回不参与去重扫描的目录（服务端自身产生的数据）。
//
// 注意不能把 UploadDir 排除掉——它是默认挂载点，属于用户数据。
func excludedPaths(cfg *config.Config) []string {
	candidates := []string{cfg.ThumbCacheDir(), cfg.TrashDir(), cfg.DBPath()}
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			out = append(out, abs)
		}
	}
	return out
}
