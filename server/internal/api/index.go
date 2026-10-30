package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/zhangjh/mini-nas/server/internal/ai"
	"github.com/zhangjh/mini-nas/server/internal/model"
	"github.com/zhangjh/mini-nas/server/internal/service"
	"github.com/zhangjh/mini-nas/server/internal/store"
)

// IndexHandler 暴露语义索引的管理接口。
type IndexHandler struct {
	svc    *service.IndexService
	engine *ai.Engine
}

func NewIndexHandler(svc *service.IndexService, engine *ai.Engine) *IndexHandler {
	return &IndexHandler{svc: svc, engine: engine}
}

// Status 返回索引覆盖率与当前任务。
//
// 与检索接口不同，这里即使 AI 未启用也返回 200：这个接口的职责就是告诉前端
// 「为什么用不了」，返回 503 只会让界面无从显示原因。
func (h *IndexHandler) Status(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: map[string]any{
			"available": false,
			"reason":    "语义检索未启用（模型未加载或当前构建不含推理能力）",
		}})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: h.svc.Status()})
}

// Scan 启动索引任务。body 可选：{"full": true} 忽略 change_token 全量重算。
func (h *IndexHandler) Scan(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAvailable(w) {
		return
	}

	var req struct {
		Full bool `json:"full"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, model.APIResponse{OK: false, Error: "invalid request body"})
			return
		}
	}

	job, err := h.svc.StartScan(req.Full)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: job})
}

// Cancel 取消正在运行的索引任务。
func (h *IndexHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAvailable(w) {
		return
	}

	var req struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{OK: false, Error: "invalid request body"})
		return
	}
	if !h.svc.Cancel(req.JobID) {
		writeJSON(w, http.StatusNotFound, model.APIResponse{OK: false, Error: "job not running"})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: map[string]any{"canceled": true}})
}

// Jobs 列出最近的索引任务。
func (h *IndexHandler) Jobs(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAvailable(w) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	jobs, err := h.svc.ListJobs(limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: jobs})
}

// Job 查询单个任务进度。
func (h *IndexHandler) Job(w http.ResponseWriter, r *http.Request) {
	if !h.ensureAvailable(w) {
		return
	}
	jobID := r.URL.Query().Get("job_id")
	if jobID == "" {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{OK: false, Error: "missing job_id"})
		return
	}
	job, err := h.svc.GetJob(jobID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, model.APIResponse{OK: false, Error: "job not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: job})
}

// Health 是部署自检入口。
//
// selftest 是必需的，不是可选的：GPU 路径出问题时不会报错，只会给出错向量，
// 没有这一项就无法在部署后发现环境变化（换驱动、换容器基底、EP 版本漂移）
// 导致的质量退化。
func (h *IndexHandler) Health(w http.ResponseWriter, r *http.Request) {
	if h.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, model.APIResponse{
			OK: false, Error: ai.ErrUnavailable.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: h.engine.Status()})
}

func (h *IndexHandler) ensureAvailable(w http.ResponseWriter) bool {
	if h.svc == nil || h.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, model.APIResponse{
			OK: false, Error: "语义检索未启用",
		})
		return false
	}
	return true
}
