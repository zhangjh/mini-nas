package api

import (
	"encoding/json"
	"net/http"

	"github.com/zhangjh/mini-nas/server/internal/model"
	"github.com/zhangjh/mini-nas/server/internal/service"
)

// ThumbHandler 暴露缩略图批量补齐相关接口。
//
// 缩略图本身由 GET /api/thumb 按需生成，这里只负责「存量补齐」这条运维链路：
// 老库或新挂载的图片往往从未被 Web 浏览过，缓存缺失；补齐后浏览与语义索引
// 都能直接命中。
type ThumbHandler struct {
	jobs *service.ThumbJobService
}

func NewThumbHandler(jobs *service.ThumbJobService) *ThumbHandler {
	return &ThumbHandler{jobs: jobs}
}

// Backfill 启动批量补齐。body 可选：{"force": true} 忽略缓存新鲜度全部重建。
func (h *ThumbHandler) Backfill(w http.ResponseWriter, r *http.Request) {
	if h.jobs == nil {
		writeJSON(w, http.StatusServiceUnavailable, model.APIResponse{
			OK: false, Error: "thumbnail service disabled",
		})
		return
	}

	var req struct {
		Force bool `json:"force"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, model.APIResponse{
				OK: false, Error: "invalid request body",
			})
			return
		}
	}

	job, err := h.jobs.Start(req.Force)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: job})
}

// Cancel 取消正在运行的补齐任务。
func (h *ThumbHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	if h.jobs == nil {
		writeJSON(w, http.StatusServiceUnavailable, model.APIResponse{
			OK: false, Error: "thumbnail service disabled",
		})
		return
	}

	var req struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "invalid request body",
		})
		return
	}
	if !h.jobs.Cancel(req.JobID) {
		writeJSON(w, http.StatusNotFound, model.APIResponse{
			OK: false, Error: "job not running",
		})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true})
}

// Status 返回缓存覆盖情况。会遍历全部挂载，大库下耗时较长。
func (h *ThumbHandler) Status(w http.ResponseWriter, r *http.Request) {
	if h.jobs == nil {
		writeJSON(w, http.StatusServiceUnavailable, model.APIResponse{
			OK: false, Error: "thumbnail service disabled",
		})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: h.jobs.Status()})
}
