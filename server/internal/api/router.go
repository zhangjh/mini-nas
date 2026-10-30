package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/zhangjh/mini-nas/server/internal/ai"
	"github.com/zhangjh/mini-nas/server/internal/config"
	"github.com/zhangjh/mini-nas/server/internal/service"
)

// Deps 汇总路由需要的全部依赖。
//
// 用结构体而不是继续加参数：路由依赖已经超过十个，位置参数一旦顺序写错
// 编译器不会报错（同类型参数可互换），排查成本极高。
type Deps struct {
	Config    *config.Config
	Files     *service.FileService
	Thumbs    *service.ThumbnailService
	Mounts    *service.MountService
	Users     *service.UserService
	Hashes    *service.HashService
	Dedup     *service.DedupService
	ThumbJobs *service.ThumbJobService
	// Index / Search / AI 三者同时为 nil（AI 未启用）或同时非 nil。
	Index  *service.IndexService
	Search *service.SearchService
	AI     *ai.Engine
}

func NewRouter(d Deps) http.Handler {
	cfg := d.Config

	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(CORSMiddleware)

	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok": true, "service": "mini-nas",
		})
	})

	authM := NewAuthMiddleware(cfg.JWTSecret)
	authH := NewAuthHandler(authM, d.Users)
	fileH := NewFileHandler(d.Files, d.Thumbs, d.Hashes, d.Index, cfg.Dedup, cfg.HostRoot)
	mountH := NewMountHandler(d.Mounts)
	dedupH := NewDedupHandler(d.Dedup, d.Hashes, cfg.Dedup.Enabled)
	thumbH := NewThumbHandler(d.ThumbJobs)
	indexH := NewIndexHandler(d.Index, d.AI)
	searchH := NewSearchHandler(d.Search)

	r.Get("/api/auth/status", authH.Status)
	r.Post("/api/auth/login", authH.Login)
	r.Post("/api/auth/register", authH.Register)

	r.Route("/api", func(r chi.Router) {
		r.Use(authM.Handler)

		r.Get("/files", fileH.List)
		r.Get("/files/tree", fileH.Tree)
		r.Get("/files/info", fileH.Info)
		r.Get("/files/search", fileH.Search)
		r.Post("/files/upload", fileH.Upload)
		r.Get("/files/download", fileH.Download)
		r.Get("/files/preview", fileH.Preview)
		r.Get("/thumb", fileH.Thumbnail)

		r.Post("/mkdir", fileH.Mkdir)
		r.Put("/file/rename", fileH.Rename)
		r.Post("/file/move", fileH.Move)
		r.Post("/file/copy", fileH.Copy)
		r.Delete("/file", fileH.Delete)

		r.Get("/system/dirs", fileH.ListSystemDirs)

		r.Get("/mounts", mountH.List)
		r.Post("/mounts", mountH.Create)
		r.Put("/mounts", mountH.Update)
		r.Delete("/mounts", mountH.Delete)

		// 内容去重
		r.Post("/dedup/scan", dedupH.Scan)
		r.Get("/dedup/status", dedupH.Status)
		r.Get("/dedup/jobs", dedupH.Jobs)
		r.Post("/dedup/cancel", dedupH.Cancel)
		r.Get("/dedup/groups", dedupH.Groups)
		r.Post("/dedup/resolve", dedupH.Resolve)
		r.Get("/dedup/stats", dedupH.Stats)
		r.Get("/dedup/skips", dedupH.Skips)

		// 缩略图存量补齐（缩略图本身由 GET /thumb 按需生成）
		r.Post("/thumb/backfill", thumbH.Backfill)
		r.Post("/thumb/cancel", thumbH.Cancel)
		r.Post("/thumb/prune", thumbH.Prune)
		r.Get("/thumb/status", thumbH.Status)

		// 语义索引
		r.Get("/index/status", indexH.Status)
		r.Get("/index/health", indexH.Health)
		r.Post("/index/scan", indexH.Scan)
		r.Post("/index/cancel", indexH.Cancel)
		r.Get("/index/jobs", indexH.Jobs)
		r.Get("/index/job", indexH.Job)

		// 语义检索
		r.Post("/search/semantic", searchH.Semantic)
		r.Post("/search/similar", searchH.Similar)
	})

	return r
}
