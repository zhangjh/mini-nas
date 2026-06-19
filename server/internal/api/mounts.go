package api

import (
	"encoding/json"
	"net/http"

	"github.com/zhangjh/mini-nas/server/internal/model"
	"github.com/zhangjh/mini-nas/server/internal/service"
)

type MountHandler struct {
	mountSvc *service.MountService
}

func NewMountHandler(mountSvc *service.MountService) *MountHandler {
	return &MountHandler{mountSvc: mountSvc}
}

func (h *MountHandler) List(w http.ResponseWriter, r *http.Request) {
	mounts := h.mountSvc.List()
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: mounts})
}

func (h *MountHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "invalid request body",
		})
		return
	}
	if req.Name == "" || req.Path == "" {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "name and path are required",
		})
		return
	}

	mount, err := h.mountSvc.Add(req.Name, req.Path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{
			OK: false, Error: err.Error(),
		})
		return
	}
	if mount == nil {
		writeJSON(w, http.StatusConflict, model.APIResponse{
			OK: false, Error: "mount name already exists",
		})
		return
	}

	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: mount})
}

func (h *MountHandler) Update(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "name parameter required",
		})
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "invalid request body",
		})
		return
	}

	mount, err := h.mountSvc.Update(name, req.Path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{
			OK: false, Error: err.Error(),
		})
		return
	}
	if mount == nil {
		writeJSON(w, http.StatusNotFound, model.APIResponse{
			OK: false, Error: "mount not found",
		})
		return
	}

	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: mount})
}

func (h *MountHandler) Delete(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		name = r.URL.Path
	}

	if !h.mountSvc.Delete(name) {
		writeJSON(w, http.StatusNotFound, model.APIResponse{
			OK: false, Error: "mount not found",
		})
		return
	}

	writeJSON(w, http.StatusOK, model.APIResponse{OK: true})
}
