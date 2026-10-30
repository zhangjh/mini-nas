package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/zhangjh/mini-nas/server/internal/config"
	"github.com/zhangjh/mini-nas/server/internal/service"
)

func NewRouter(
	cfg *config.Config,
	fileSvc *service.FileService,
	thumbSvc *service.ThumbnailService,
	mountSvc *service.MountService,
	userSvc *service.UserService,
	hashSvc *service.HashService,
	dedupSvc *service.DedupService,
) http.Handler {
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
	authH := NewAuthHandler(authM, userSvc)
	fileH := NewFileHandler(fileSvc, thumbSvc, hashSvc, cfg.Dedup, cfg.HostRoot)
	mountH := NewMountHandler(mountSvc)
	dedupH := NewDedupHandler(dedupSvc, hashSvc, cfg.Dedup.Enabled)

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
	})

	return r
}
