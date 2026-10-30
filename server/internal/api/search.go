package api

import (
	"encoding/json"
	"net/http"

	"github.com/zhangjh/mini-nas/server/internal/model"
	"github.com/zhangjh/mini-nas/server/internal/service"
)

// maxSearchLimit 是单次检索的结果上限。
const maxSearchLimit = 200

// SearchHandler 暴露语义检索接口。
type SearchHandler struct {
	svc *service.SearchService
}

func NewSearchHandler(svc *service.SearchService) *SearchHandler {
	return &SearchHandler{svc: svc}
}

// Semantic 文搜图：POST /api/search/semantic
//
//	{"query": "海边日落", "limit": 50, "min_score": 0.18}
func (h *SearchHandler) Semantic(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAvailable(w) {
		return
	}

	var req struct {
		Query    string  `json:"query"`
		Limit    int     `json:"limit"`
		MinScore float64 `json:"min_score"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{OK: false, Error: "invalid request body"})
		return
	}
	if req.Query == "" {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{OK: false, Error: "query 不能为空"})
		return
	}

	result, err := h.svc.TextSearch(req.Query, clampLimit(req.Limit), req.MinScore)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: result})
}

// Similar 以图搜图：POST /api/search/similar
//
//	{"path": "照片/2024/IMG_1234.jpg", "limit": 50, "min_score": 0.75}
func (h *SearchHandler) Similar(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAvailable(w) {
		return
	}

	var req struct {
		Path     string  `json:"path"`
		Limit    int     `json:"limit"`
		MinScore float64 `json:"min_score"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{OK: false, Error: "invalid request body"})
		return
	}
	if req.Path == "" {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{OK: false, Error: "path 不能为空"})
		return
	}

	result, err := h.svc.SimilarByPath(req.Path, clampLimit(req.Limit), req.MinScore)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: result})
}

func (h *SearchHandler) ensureAvailable(w http.ResponseWriter) bool {
	if h.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, model.APIResponse{
			OK: false, Error: "语义检索未启用",
		})
		return false
	}
	return true
}

func clampLimit(n int) int {
	if n <= 0 {
		return 0 // 交给 service 用默认值
	}
	if n > maxSearchLimit {
		return maxSearchLimit
	}
	return n
}
