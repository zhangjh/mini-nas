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

	"github.com/zhangjh/mini-nas/server/internal/ai"
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

	// 必须在 -data 覆盖之后再解析，否则容器里会指向 /app/data。
	// 结果只放在局部变量里，不写回 cfg：cfg.Save 会把绝对路径固化进
	// 配置文件，而配置文件本身在数据目录里，容器写 /data/... 后宿主机
	// 本地开发就会读到这条路径。
	modelDir := resolveModelDir(cfg.AI.ModelDir, cfg.DataDir, cfg.AI.ModelID)

	// 只在配置文件不存在时生成模板。
	//
	// 以前每次启动都把完整配置写回，会把「当时的代码默认值」固化成文件值：
	// 之后升级改了默认值（比如检索阈值），老实例上永远不生效，而且没有任何提示
	// ——线上就踩过一次，改了默认阈值却依然按旧值检索。
	//
	// 首次部署仍然要落盘：jwt_secret 是随机生成的，不落盘重启后所有 token 失效。
	if _, statErr := os.Stat(absConfigPath); os.IsNotExist(statErr) {
		if err := cfg.Save(absConfigPath); err != nil {
			log.Printf("Warning: could not save config: %v", err)
		} else {
			log.Printf("Generated default config: %s", absConfigPath)
		}
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
		cfg.Thumbnail.BackfillWorkers, cfg.Thumbnail.PruneOrphans, cfg.DataDir,
	)

	// 语义检索：模型或推理运行库缺失时降级为 503，不影响其余功能启动。
	// 这与「去重关闭」是同一种降级语义，客户端只需处理一种失败形态。
	var (
		aiEngine  *ai.Engine
		indexSvc  *service.IndexService
		searchSvc *service.SearchService
	)
	if cfg.AI.Enabled {
		log.Printf("语义检索: model_dir=%s device=%s precision=%s", modelDir, cfg.AI.Device, cfg.AI.Precision)
		engine, err := ai.New(ai.Config{
			ModelDir:      modelDir,
			ModelID:       cfg.AI.ModelID,
			Device:        cfg.AI.Device,
			Precision:     cfg.AI.Precision,
			ORTLib:        cfg.AI.ORTLib,
			Threads:       cfg.AI.Threads,
			SelfTest:      cfg.AI.SelfTest,
			TextCacheSize: cfg.AI.TextCacheSize,
		})
		if err != nil {
			log.Printf("WARN 语义检索未启用: %v", err)
		} else {
			aiEngine = engine
			defer engine.Close()

			vecIdx := service.NewVectorIndex(engine.Dim())
			indexSvc = service.NewIndexService(db, engine, mountSvc, fileSvc, thumbSvc, vecIdx, service.IndexOptions{
				ModelID:            cfg.AI.ModelID,
				Extensions:         cfg.AI.Extensions,
				ThrottleMS:         cfg.AI.ThrottleMS,
				BatchSize:          cfg.AI.BatchSize,
				PreferThumbnail:    cfg.AI.PreferThumbnail,
				WritebackThumbnail: cfg.AI.WritebackThumbnail,
				DataDir:            cfg.DataDir,
			})
			searchSvc = service.NewSearchService(engine, db, fileSvc, vecIdx, service.SearchOptions{
				DefaultLimit:  cfg.AI.Search.DefaultLimit,
				TextMinScore:  cfg.AI.Search.TextMinScore,
				ImageMinScore: cfg.AI.Search.ImageMinScore,
			})
			// 后台预加载向量索引，避免首次检索冷启动。
			searchSvc.Warmup()
		}
	}

	apiRouter := api.NewRouter(api.Deps{
		Config:    cfg,
		Files:     fileSvc,
		Thumbs:    thumbSvc,
		Mounts:    mountSvc,
		Users:     userSvc,
		Hashes:    hashSvc,
		Dedup:     dedupSvc,
		ThumbJobs: thumbJobs,
		Index:     indexSvc,
		Search:    searchSvc,
		AI:        aiEngine,
	})
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

// resolveModelDir 决定模型产物目录（含 meta.json / vocab.txt / 两个 onnx）。
//
// 默认是 <data_dir>/models/<model_id>：docker-compose 把 ./server/models 挂到
// /data/models，零配置即可生效。
//
// 相对路径先按工作目录解析，再按数据目录解析。两个场景共用同一份 config.json：
//   - 本地 `make dev-server` 在 server/ 下启动，模型在 server/models，第一候选命中；
//   - 容器内工作目录是 /app，模型在 /data/models，第一候选不存在，第二候选命中。
//
// 两个候选都不存在时原样返回，让引擎报「模型文件缺失」并降级为 503，
// 而不是在这里静默指向一个空目录。
func resolveModelDir(configured, dataDir, modelID string) string {
	if configured == "" {
		return filepath.Join(dataDir, "models", modelID)
	}
	if filepath.IsAbs(configured) {
		return configured
	}
	for _, c := range []string{configured, filepath.Join(dataDir, configured)} {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			if abs, err := filepath.Abs(c); err == nil {
				return abs
			}
			return c
		}
	}
	return configured
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
