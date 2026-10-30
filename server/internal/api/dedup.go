package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/zhangjh/mini-nas/server/internal/model"
	"github.com/zhangjh/mini-nas/server/internal/service"
	"github.com/zhangjh/mini-nas/server/internal/store"
)

// DedupHandler 暴露内容去重相关的接口：存量扫描、分组查询、清理执行。
type DedupHandler struct {
	dedupSvc *service.DedupService
	hashSvc  *service.HashService
	enabled  bool
}

func NewDedupHandler(dedupSvc *service.DedupService, hashSvc *service.HashService, enabled bool) *DedupHandler {
	return &DedupHandler{dedupSvc: dedupSvc, hashSvc: hashSvc, enabled: enabled}
}

// Scan 启动全库扫描。body 可选：{"path": "照片"} 指定扫描范围。
func (h *DedupHandler) Scan(w http.ResponseWriter, r *http.Request) {
	if !h.ensureEnabled(w) {
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, model.APIResponse{OK: false, Error: "invalid request body"})
			return
		}
	}

	job, err := h.dedupSvc.StartScan(req.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: job})
}

// Status 查询扫描任务进度。
func (h *DedupHandler) Status(w http.ResponseWriter, r *http.Request) {
	if !h.ensureEnabled(w) {
		return
	}
	jobID := r.URL.Query().Get("job_id")
	if jobID == "" {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{OK: false, Error: "missing job_id"})
		return
	}
	job, err := h.dedupSvc.GetJob(jobID)
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

// Jobs 列出最近的扫描任务。
func (h *DedupHandler) Jobs(w http.ResponseWriter, r *http.Request) {
	if !h.ensureEnabled(w) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	jobs, err := h.dedupSvc.ListJobs(limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: jobs})
}

// Cancel 取消正在运行的扫描。
func (h *DedupHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	if !h.ensureEnabled(w) {
		return
	}
	jobID := r.URL.Query().Get("job_id")
	if jobID == "" {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{OK: false, Error: "missing job_id"})
		return
	}
	if !h.dedupSvc.CancelScan(jobID) {
		writeJSON(w, http.StatusNotFound, model.APIResponse{OK: false, Error: "job not running"})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: map[string]any{"canceled": true}})
}

// Groups 分页列出重复分组。
func (h *DedupHandler) Groups(w http.ResponseWriter, r *http.Request) {
	if !h.ensureEnabled(w) {
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	minReclaim, _ := strconv.ParseInt(r.URL.Query().Get("min_reclaim"), 10, 64)

	groups, total, err := h.dedupSvc.ListGroups(page, pageSize, minReclaim)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: map[string]any{
		"items":     groups,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	}})
}

// Resolve 执行清理：合并为硬链接或移入回收站。
func (h *DedupHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	if !h.ensureEnabled(w) {
		return
	}

	var req service.ResolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{OK: false, Error: "invalid request body"})
		return
	}

	result, err := h.dedupSvc.Resolve(req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: result})
}

// Stats 汇总重复分组与去重跳过的情况。
func (h *DedupHandler) Stats(w http.ResponseWriter, r *http.Request) {
	if !h.ensureEnabled(w) {
		return
	}
	groups, skips, err := h.dedupSvc.Stats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: map[string]any{
		"groups":      groups,
		"upload_skip": skips,
	}})
}

// Skips 列出「内容已去重、未落盘」的虚拟条目，用于排查同步异常。
func (h *DedupHandler) Skips(w http.ResponseWriter, r *http.Request) {
	if !h.ensureEnabled(w) {
		return
	}
	prefix := r.URL.Query().Get("path")
	items, err := h.hashSvc.ListSkips(prefix)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: items})
}

func (h *DedupHandler) ensureEnabled(w http.ResponseWriter) bool {
	if !h.enabled {
		writeJSON(w, http.StatusServiceUnavailable, model.APIResponse{OK: false, Error: "内容去重未启用"})
		return false
	}
	return true
}
