package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zhangjh/mini-nas/server/internal/model"
	"github.com/zhangjh/mini-nas/server/internal/service"
)

type FileHandler struct {
	fileSvc  *service.FileService
	thumbSvc *service.ThumbnailService
	hostRoot string // 宿主机根目录在容器内的映射路径，为空则直接用原始路径
}

func NewFileHandler(fileSvc *service.FileService, thumbSvc *service.ThumbnailService, hostRoot string) *FileHandler {
	return &FileHandler{fileSvc: fileSvc, thumbSvc: thumbSvc, hostRoot: hostRoot}
}

func (h *FileHandler) List(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	sortBy := r.URL.Query().Get("sort_by")
	sortDesc := r.URL.Query().Get("sort_desc") == "true"

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 0
	}

	fl, err := h.fileSvc.ListDir(path, page, pageSize, sortBy, sortDesc)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{
			OK: false, Error: err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: fl})
}

func (h *FileHandler) Info(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	info, err := h.fileSvc.GetFileInfo(path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, model.APIResponse{
			OK: false, Error: "file not found",
		})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: info})
}

// Tree returns a flat list of all files (not directories) recursively under
// the given path. Used by the Android sync client to skip files that already
// exist on the server when the local manifest is empty.
func (h *FileHandler) Tree(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	files, err := h.fileSvc.ListTree(path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{
			OK: false, Error: err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: files})
}

func (h *FileHandler) Upload(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = r.FormValue("path")
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "invalid multipart form",
		})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "missing file field",
		})
		return
	}
	defer file.Close()

	targetPath := filepath.Join(path, header.Filename)
	written, err := h.fileSvc.WriteFile(targetPath, file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{
			OK: false, Error: err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, model.APIResponse{
		OK: true,
		Data: map[string]interface{}{
			"size":  written,
			"path":  targetPath,
			"name":  header.Filename,
		},
	})
}

func (h *FileHandler) Download(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")

	h.fileSvc.RLockFile(path)
	defer h.fileSvc.RUnlockFile(path)

	f, err := h.fileSvc.OpenFile(path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, model.APIResponse{
			OK: false, Error: "file not found",
		})
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusNotFound, model.APIResponse{
			OK: false, Error: "file not found",
		})
		return
	}
	if info.IsDir() {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "cannot download a directory",
		})
		return
	}
	name := filepath.Base(path)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")

	n, err := io.Copy(w, f)
	if err != nil {
		log.Printf("download error (path=%s): sent %d bytes, err: %v", path, n, err)
	}
}

func (h *FileHandler) Preview(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")

	h.fileSvc.RLockFile(path)
	defer h.fileSvc.RUnlockFile(path)

	f, err := h.fileSvc.OpenFile(path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, model.APIResponse{
			OK: false, Error: "file not found",
		})
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		writeJSON(w, http.StatusNotFound, model.APIResponse{
			OK: false, Error: "file not found",
		})
		return
	}
	if info.IsDir() {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "cannot preview a directory",
		})
		return
	}

	// Use http.ServeContent to support HTTP Range requests (required for
	// video/audio seeking). ServeContent handles Content-Length, Range,
	// 206 Partial Content, and conditional requests automatically.
	mime := detectMimeType(path)
	w.Header().Set("Content-Type", mime)
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

func (h *FileHandler) Thumbnail(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")

	// 读锁：防止并发写入源文件时生成缩略图读到不完整数据
	h.fileSvc.RLockFile(path)
	defer h.fileSvc.RUnlockFile(path)

	absPath := h.fileSvc.AbsPath(path)

	thumbPath, err := h.thumbSvc.GetThumbnail(absPath)
	if err != nil || thumbPath == "" {
		writeJSON(w, http.StatusNotFound, model.APIResponse{
			OK: false, Error: "thumbnail not available",
		})
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, thumbPath)
}

func (h *FileHandler) Mkdir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "invalid request",
		})
		return
	}
	if err := h.fileSvc.Mkdir(req.Path); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{
			OK: false, Error: err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true})
}

func (h *FileHandler) Delete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "invalid request",
		})
		return
	}
	if err := h.fileSvc.Delete(req.Path); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{
			OK: false, Error: err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true})
}

func (h *FileHandler) Rename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "invalid request",
		})
		return
	}
	dir := filepath.Dir(req.Path)
	newPath := filepath.Join(dir, req.NewName)
	if err := h.fileSvc.Rename(req.Path, newPath); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{
			OK: false, Error: err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true})
}

func (h *FileHandler) Move(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths []string `json:"paths"`
		Dest  string   `json:"dest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "invalid request",
		})
		return
	}
	if err := h.fileSvc.Move(req.Paths, req.Dest); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{
			OK: false, Error: err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true})
}

func (h *FileHandler) Copy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths []string `json:"paths"`
		Dest  string   `json:"dest"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "invalid request",
		})
		return
	}
	if err := h.fileSvc.Copy(req.Paths, req.Dest); err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{
			OK: false, Error: err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true})
}

func (h *FileHandler) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	path := r.URL.Query().Get("path")
	if q == "" {
		writeJSON(w, http.StatusBadRequest, model.APIResponse{
			OK: false, Error: "missing query parameter q",
		})
		return
	}

	results := h.fileSvc.Search(q, path)
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: results})
}

type DirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

func (h *FileHandler) ListSystemDirs(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}

	// 容器环境下通过 hostRoot 映射宿主机路径，前端无感知
	// 前端传 /mnt，实际读取 /host/mnt，但返回给前端的路径仍是 /mnt
	hostPath := path
	if h.hostRoot != "" {
		hostPath = filepath.Join(h.hostRoot, path)
	}

	entries, err := os.ReadDir(hostPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, model.APIResponse{
			OK: false, Error: err.Error(),
		})
		return
	}

	var dirs []DirEntry
	for _, entry := range entries {
		if entry.IsDir() {
			name := entry.Name()
			if name == "." || name == ".." {
				continue
			}
			// 返回给前端的路径是原始路径（不带 /host 前缀）
			dirPath := pathJoin(path, name)
			dirs = append(dirs, DirEntry{Name: name, Path: dirPath})
		}
	}

	writeJSON(w, http.StatusOK, model.APIResponse{OK: true, Data: dirs})
}

func pathJoin(base, name string) string {
	if base == "/" {
		return "/" + name
	}
	return base + "/" + name
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func detectMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	case ".mp4":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".pdf":
		return "application/pdf"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".ogg":
		return "audio/ogg"
	case ".aac":
		return "audio/aac"
	case ".txt", ".md":
		return "text/plain; charset=utf-8"
	case ".log":
		return "text/plain; charset=utf-8"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".csv":
		return "text/csv; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func isVideo(mime string) bool {
	return len(mime) >= 6 && mime[:6] == "video/"
}

func isAudio(mime string) bool {
	return len(mime) >= 6 && mime[:6] == "audio/"
}
