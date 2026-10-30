package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	vpath "path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zhangjh/mini-nas/server/internal/config"
	"github.com/zhangjh/mini-nas/server/internal/model"
	"github.com/zhangjh/mini-nas/server/internal/service"
)

// writeFileError 把文件服务返回的错误映射成合适的状态码。
//
// 之前所有错误一律回 500，把「客户端传错路径」和「服务端真出故障」混为一谈，
// 前端只能显示一句无信息量的错误。挂载点不存在属于请求错误（400）——
// 典型场景是在根视图（没有选中挂载）直接上传或建目录；
// 路径不存在属于资源不存在（404）。
func writeFileError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, service.ErrMountNotFound):
		status = http.StatusBadRequest
	case errors.Is(err, os.ErrNotExist):
		status = http.StatusNotFound
	}
	writeJSON(w, status, model.APIResponse{OK: false, Error: err.Error()})
}

type FileHandler struct {
	fileSvc  *service.FileService
	thumbSvc *service.ThumbnailService
	hashSvc  *service.HashService
	// indexSvc 可为 nil（AI 未启用）：文件删除/改名时同步清理语义索引。
	indexSvc *service.IndexService
	dedupCfg config.DedupConfig
	hostRoot string // 宿主机根目录在容器内的映射路径，为空则直接用原始路径
}

func NewFileHandler(
	fileSvc *service.FileService,
	thumbSvc *service.ThumbnailService,
	hashSvc *service.HashService,
	indexSvc *service.IndexService,
	dedupCfg config.DedupConfig,
	hostRoot string,
) *FileHandler {
	return &FileHandler{
		fileSvc:  fileSvc,
		thumbSvc: thumbSvc,
		hashSvc:  hashSvc,
		indexSvc: indexSvc,
		dedupCfg: dedupCfg,
		hostRoot: hostRoot,
	}
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
		writeFileError(w, err)
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
		writeFileError(w, err)
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

	targetPath := vpath.Join(path, header.Filename)

	// 先落临时文件并算出 MD5：去重判定需要完整内容指纹，只有读完全部
	// 字节才能得到。暂存/提交分离避免了重复读取上传流。
	stage, err := h.fileSvc.StageFile(targetPath, file)
	if err != nil {
		writeFileError(w, err)
		return
	}
	defer stage.Discard()

	// 内容去重：仅在目标路径原本不存在时生效。覆盖同名文件属于正常重传，
	// 不应被判定为「重复」。
	if h.dedupCfg.Enabled && h.dedupCfg.SkipUploadDuplicate &&
		h.hashSvc != nil && !h.fileSvc.Exists(targetPath) {
		if existing, ok := h.hashSvc.FindDuplicate(stage.MD5, stage.Size, targetPath); ok {
			stage.Discard()
			mtime := time.Now().UnixMilli()
			if err := h.hashSvc.RecordSkip(targetPath, stage.MD5, stage.Size, mtime, existing); err != nil {
				log.Printf("record dedup skip for %s: %v", targetPath, err)
			}
			writeJSON(w, http.StatusOK, model.APIResponse{
				OK: true,
				Data: map[string]interface{}{
					"size":         stage.Size,
					"path":         targetPath,
					"name":         header.Filename,
					"md5":          stage.MD5,
					"duplicate":    true,
					"duplicate_of": existing,
				},
			})
			return
		}
	}

	if err := stage.Commit(); err != nil {
		writeFileError(w, err)
		return
	}

	if h.hashSvc != nil {
		_ = h.hashSvc.Record(targetPath, stage.MD5, stage.Size, time.Now().UnixMilli())
		// 该路径此前可能登记过「已去重未落盘」，现在真实落盘了，记录作废。
		_ = h.hashSvc.ForgetSkip(targetPath)
	}

	writeJSON(w, http.StatusOK, model.APIResponse{
		OK: true,
		Data: map[string]interface{}{
			"size":      stage.Size,
			"path":      targetPath,
			"name":      header.Filename,
			"md5":       stage.MD5,
			"duplicate": false,
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

	thumbPath, err := h.thumbSvc.Get(absPath)
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
		writeFileError(w, err)
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
		writeFileError(w, err)
		return
	}
	h.invalidateFingerprints(req.Path)
	h.invalidateThumbs(req.Path)
	h.invalidateAssetsUnder(req.Path)
	writeJSON(w, http.StatusOK, model.APIResponse{OK: true})
}

// invalidateFingerprints 在路径发生删除/改名/移动后清除相关的内容指纹。
//
// 指纹表以虚拟路径为键，路径一变旧记录就失去意义；留着会让上传去重
// 命中一个已不存在的「原件」。按前缀删除同时覆盖目录删除的情况。
func (h *FileHandler) invalidateFingerprints(virtualPath string) {
	if h.hashSvc == nil {
		return
	}
	if err := h.hashSvc.ForgetUnder(virtualPath); err != nil {
		log.Printf("forget fingerprints under %s: %v", virtualPath, err)
	}
	if err := h.hashSvc.ForgetSkip(virtualPath); err != nil {
		log.Printf("forget skip %s: %v", virtualPath, err)
	}
}

// invalidateThumbs 清除一批虚拟路径对应的缩略图缓存。
//
// 缩略图以源文件绝对路径的哈希为键，所以要先经挂载点解析。
// 注意这里只处理「路径本身」：目录删除时无法枚举其下的文件，残留缓存
// 由 backfill 的 PruneOrphans 兜底清理。
//
// 改名/移动必须显式失效：os.Rename 会保留源文件的 mtime，若目标路径上
// 已有更晚生成的缓存，仅靠 mtime 校验会误判为「新鲜」，从而返回旧图。
func (h *FileHandler) invalidateThumbs(virtualPaths ...string) {
	if h.thumbSvc == nil {
		return
	}
	for _, p := range virtualPaths {
		if p == "" {
			continue
		}
		if abs := h.fileSvc.AbsPath(p); abs != "" {
			h.thumbSvc.Invalidate(abs)
		}
	}
}

// invalidateAssetsUnder 在删除后清理语义索引。
//
// 索引与文件系统没有事务：不清理的话，检索仍会命中已删除的文件，
// 用户点开就是 404。目录删除时按前缀整棵清理。
func (h *FileHandler) invalidateAssetsUnder(virtualPath string) {
	if h.indexSvc == nil || virtualPath == "" {
		return
	}
	h.indexSvc.ForgetUnder(virtualPath)
}

// renameAsset 让索引跟随改名/移动。
func (h *FileHandler) renameAsset(oldPath, newPath string) {
	if h.indexSvc == nil || oldPath == "" || newPath == "" {
		return
	}
	h.indexSvc.RenamePath(oldPath, newPath)
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
		writeFileError(w, err)
		return
	}
	h.invalidateFingerprints(req.Path)
	h.invalidateThumbs(req.Path, newPath)
	h.renameAsset(req.Path, newPath)
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
		writeFileError(w, err)
		return
	}
	for _, p := range req.Paths {
		h.invalidateFingerprints(p)
		// Move 的 dst 恒为 dest 目录下的同名文件
		dst := filepath.Join(req.Dest, filepath.Base(p))
		h.invalidateThumbs(p, dst)
		h.renameAsset(p, dst)
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
		writeFileError(w, err)
		return
	}
	// 复制会在目标目录生成新文件，若该路径此前有缓存需一并失效
	// （复制实现可能保留源 mtime，仅靠 mtime 校验不足以判定新鲜）。
	for _, p := range req.Paths {
		h.invalidateThumbs(filepath.Join(req.Dest, filepath.Base(p)))
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
		writeFileError(w, err)
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
