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
)

func main() {
	configPath := flag.String("config", "config.json", "path to configuration file")
	port := flag.Int("port", 0, "port to listen on")
	dataDir := flag.String("data", "", "data directory path")
	password := flag.String("password", "", "set or override login password")
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
	if *password != "" {
		cfg.Password = *password
	}

	if err := cfg.Save(absConfigPath); err != nil {
		log.Printf("Warning: could not save config: %v", err)
	}

	mountSvc := service.NewMountService(cfg.DBPath())

	fileSvc := service.NewFileService(mountSvc)
	thumbSvc := service.NewThumbnailService(cfg.ThumbCacheDir())

	apiRouter := api.NewRouter(cfg, fileSvc, thumbSvc, mountSvc)
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
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
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
