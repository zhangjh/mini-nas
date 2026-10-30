package service

import (
	"context"
	"fmt"
	"image"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/zhangjh/mini-nas/server/internal/ai"
	"github.com/zhangjh/mini-nas/server/internal/imgcodec"
	"github.com/zhangjh/mini-nas/server/internal/model"
	"github.com/zhangjh/mini-nas/server/internal/store"
)

// JobTypeIndexScan 是语义索引任务类型。
const JobTypeIndexScan = "index_scan"

// IndexOptions 控制索引流水线。
type IndexOptions struct {
	// ModelID 是向量归属标识，换模型不删旧向量，靠它隔离。
	ModelID string
	// Extensions 参与索引的扩展名（含点，小写）。
	Extensions []string
	// ThrottleMS 是每张图推理后的休眠，用于给 Web 请求让路。
	ThrottleMS int
	// BatchSize 是落库批量。
	BatchSize int
	// PreferThumbnail 为 true 时优先用缩略图索引（零信息损失，快 24 倍）。
	PreferThumbnail bool
	// WritebackThumbnail 为 true 时，解原图的顺便回写缩略图缓存。
	WritebackThumbnail bool
	// DataDir 用于遍历时整棵跳过服务端自身数据。
	DataDir string
}

// IndexStatus 是索引覆盖率与运行状态。
type IndexStatus struct {
	Available bool       `json:"available"`
	ModelID   string     `json:"model_id"`
	Device    string     `json:"device"`
	Total     int64      `json:"total"`
	Indexed   int64      `json:"indexed"`
	Pending   int64      `json:"pending"`
	Failed    int64      `json:"failed"`
	Vectors   int        `json:"vectors"`
	Running   *model.Job `json:"running"`
}

// IndexService 是索引流水线的编排者。
//
// 流水线（design §7.1）：遍历 → 变更检测 → 解码+预处理 → 推理 → 批量落库 → 进度上报。
// 并发模型（§7.3）：每级各一个 goroutine，用容量 8 的有界 channel 形成背压。
// **不要盲目加线程**——解码只占 4.7~17ms，推理 121ms，两者抢同一颗 die 的功耗预算，
// 实测加解码线程后端到端反而变慢。
type IndexService struct {
	db     *store.DB
	engine *ai.Engine
	mounts *MountService
	files  *FileService
	thumbs *ThumbnailService
	idx    *VectorIndex
	opts   IndexOptions

	exts     map[string]struct{}
	skipDirs []os.FileInfo

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

func NewIndexService(
	db *store.DB,
	engine *ai.Engine,
	mounts *MountService,
	files *FileService,
	thumbs *ThumbnailService,
	idx *VectorIndex,
	opts IndexOptions,
) *IndexService {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 64
	}
	exts := make(map[string]struct{}, len(opts.Extensions))
	for _, e := range opts.Extensions {
		exts[normalizeExtension(e)] = struct{}{}
	}

	// 缩略图缓存与数据目录可能落在某个挂载点内部（本机部署把仓库放在
	// /mnt/nas/dev 下，data 目录随之被 dev 挂载覆盖）。不跳过的话，
	// 缓存里的缩略图会被当成源图片再索引一遍，索引会自我膨胀。
	var skipDirs []os.FileInfo
	for _, d := range []string{thumbs.CacheDir, opts.DataDir} {
		if d == "" {
			continue
		}
		if info, err := os.Stat(d); err == nil {
			skipDirs = append(skipDirs, info)
		}
	}

	return &IndexService{
		db:       db,
		engine:   engine,
		mounts:   mounts,
		files:    files,
		thumbs:   thumbs,
		idx:      idx,
		opts:     opts,
		exts:     exts,
		skipDirs: skipDirs,
		running:  map[string]context.CancelFunc{},
	}
}

// StartScan 启动一次索引。已有任务在跑时直接返回该任务。
func (s *IndexService) StartScan(full bool) (*model.Job, error) {
	if s.engine == nil {
		return nil, ai.ErrUnavailable
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for jobID := range s.running {
		if job, err := s.db.GetJob(jobID); err == nil {
			return job, nil
		}
	}

	job := &model.Job{
		ID:        uuid.NewString(),
		Type:      JobTypeIndexScan,
		State:     model.JobPending,
		Message:   "准备遍历图片",
		StartedAt: time.Now().UnixMilli(),
	}
	if _, err := s.db.CreateJob(*job); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.running[job.ID] = cancel

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.running, job.ID)
			s.mu.Unlock()
			cancel()
		}()
		if err := s.run(ctx, job.ID, full); err != nil {
			log.Printf("index scan %s failed: %v", job.ID, err)
			_ = s.db.FailJob(job.ID, err.Error())
		}
	}()

	return job, nil
}

// Cancel 取消指定任务。
func (s *IndexService) Cancel(jobID string) bool {
	s.mu.Lock()
	cancel, ok := s.running[jobID]
	s.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// GetJob 读取任务状态。
func (s *IndexService) GetJob(jobID string) (*model.Job, error) {
	return s.db.GetJob(jobID)
}

// ListJobs 列出最近的索引任务。
func (s *IndexService) ListJobs(limit int) ([]model.Job, error) {
	return s.db.ListJobs(JobTypeIndexScan, limit)
}

// Status 汇总索引覆盖情况。
func (s *IndexService) Status() IndexStatus {
	st := IndexStatus{Available: s.engine != nil, ModelID: s.opts.ModelID, Vectors: s.idx.Len()}
	if s.engine != nil {
		st.Device = s.engine.Status().Device
	}
	if counts, err := s.db.IndexCounts(s.opts.ModelID); err == nil {
		st.Total = counts.Total
		st.Indexed = counts.Indexed
		st.Pending = counts.Pending
		st.Failed = counts.Failed
	}

	s.mu.Lock()
	for jobID := range s.running {
		if job, err := s.db.GetJob(jobID); err == nil {
			st.Running = job
			break
		}
	}
	s.mu.Unlock()
	return st
}

// ForgetPaths 在文件被删除后同步清理索引。
func (s *IndexService) ForgetPaths(paths ...string) {
	if len(paths) == 0 {
		return
	}
	if err := s.db.DeleteAssets(paths); err != nil {
		log.Printf("forget assets: %v", err)
	}
	s.idx.RemovePaths(paths)
}

// ForgetUnder 清理某个目录前缀下的索引（目录被删除时）。
func (s *IndexService) ForgetUnder(prefix string) {
	if prefix == "" {
		return
	}
	if err := s.db.DeleteAssetsUnder(prefix); err != nil {
		log.Printf("forget assets under %s: %v", prefix, err)
	}
	s.idx.RemoveUnder(prefix)
}

// RenamePath 跟随改名/移动更新索引。
func (s *IndexService) RenamePath(oldPath, newPath string) {
	if oldPath == "" || newPath == "" {
		return
	}
	if err := s.db.RenameAsset(oldPath, newPath); err != nil {
		log.Printf("rename asset %s -> %s: %v", oldPath, newPath, err)
	}
	s.idx.RenamePath(oldPath, newPath)
}

// ---- 流水线 ----

type indexCandidate struct {
	virtual string
	abs     string
	size    int64
	mtime   int64
	bucket  string
}

type indexTask struct {
	candidate indexCandidate
	token     string
}

type preparedImage struct {
	task   indexTask
	tensor []float32
	width  int
	height int
	source string
	err    error
}

type indexResult struct {
	task   indexTask
	vec    []float32
	width  int
	height int
	source string
	err    error
}

type indexStats struct {
	total   int64
	done    atomic.Int64
	indexed atomic.Int64
	failed  atomic.Int64
	skipped atomic.Int64
}

func (s *IndexService) run(ctx context.Context, jobID string, full bool) error {
	if err := s.db.SetJobState(jobID, model.JobRunning, "遍历图片"); err != nil {
		return err
	}

	candidates, walkedMounts := s.walk(func(n int) {
		_ = s.db.UpdateJobProgress(jobID, 0, 0, fmt.Sprintf("已发现 %d 张图片", n))
	})
	total := int64(len(candidates))
	_ = s.db.UpdateJobProgress(jobID, 0, total, "开始索引")

	snapshot, err := s.db.AssetSnapshot(s.opts.ModelID)
	if err != nil {
		return err
	}

	stats := &indexStats{total: total}
	reportStop := make(chan struct{})
	go s.reportLoop(jobID, stats, reportStop)
	defer close(reportStop)

	tasks := make(chan indexTask, 8)
	prepared := make(chan preparedImage, 8)
	results := make(chan indexResult, s.opts.BatchSize)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer close(prepared)
		s.decodeLoop(ctx, tasks, prepared)
	}()
	go func() {
		defer wg.Done()
		defer close(results)
		s.encodeLoop(ctx, prepared, results)
	}()

	writeDone := make(chan error, 1)
	go func() { writeDone <- s.writeLoop(results, stats) }()

	// 喂料：变更检测在这里做，跳过的文件不进入流水线。
	for _, c := range candidates {
		if ctx.Err() != nil {
			break
		}
		token := changeToken(c.size, c.mtime)
		if st, ok := snapshot[c.virtual]; ok && !full && st.HasVector && st.ChangeToken == token {
			stats.skipped.Add(1)
			stats.done.Add(1)
			continue
		}
		select {
		case <-ctx.Done():
		case tasks <- indexTask{candidate: c, token: token}:
		}
	}
	close(tasks)
	wg.Wait()
	writeErr := <-writeDone

	if ctx.Err() != nil {
		_ = s.db.UpdateJobProgress(jobID, stats.done.Load(), total, "已取消")
		_ = s.db.SetJobState(jobID, model.JobCanceled, "已取消")
		return nil
	}
	if writeErr != nil {
		return writeErr
	}

	// 清理幽灵记录：源文件已删除或改名后，向量仍在库里。
	keep := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		keep[c.virtual] = struct{}{}
	}
	pruned, err := s.db.PruneAssets(keep, walkedMounts)
	if err != nil {
		log.Printf("prune media assets: %v", err)
	} else {
		for _, id := range pruned {
			s.idx.Remove(id)
		}
	}

	msg := fmt.Sprintf("完成：成功 %d，跳过 %d，失败 %d，清理失效 %d",
		stats.indexed.Load(), stats.skipped.Load(), stats.failed.Load(), len(pruned))
	_ = s.db.UpdateJobProgress(jobID, stats.done.Load(), total, msg)
	return s.db.SetJobState(jobID, model.JobDone, msg)
}

func (s *IndexService) reportLoop(jobID string, stats *indexStats, stop <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			_ = s.db.UpdateJobProgress(jobID, stats.done.Load(), stats.total,
				fmt.Sprintf("已处理 %d/%d（成功 %d，跳过 %d，失败 %d）",
					stats.done.Load(), stats.total,
					stats.indexed.Load(), stats.skipped.Load(), stats.failed.Load()))
		}
	}
}

// decodeLoop 负责解码与预处理（CPU 密集）。
func (s *IndexService) decodeLoop(ctx context.Context, tasks <-chan indexTask, out chan<- preparedImage) {
	for t := range tasks {
		if ctx.Err() != nil {
			continue // 排空 channel，避免上游阻塞
		}
		img, source, err := s.loadImage(t.candidate)
		if err != nil {
			out <- preparedImage{task: t, err: err}
			continue
		}
		b := img.Bounds()
		out <- preparedImage{
			task:   t,
			tensor: s.engine.Preprocess(img),
			width:  b.Dx(),
			height: b.Dy(),
			source: source,
		}
	}
}

// encodeLoop 负责推理（GPU 串行）。
func (s *IndexService) encodeLoop(ctx context.Context, in <-chan preparedImage, out chan<- indexResult) {
	for p := range in {
		if ctx.Err() != nil {
			continue
		}
		if p.err != nil {
			out <- indexResult{task: p.task, err: p.err}
			continue
		}
		vec, err := s.engine.EncodeTensor(p.tensor)
		out <- indexResult{
			task:   p.task,
			vec:    vec,
			width:  p.width,
			height: p.height,
			source: p.source,
			err:    err,
		}
		if s.opts.ThrottleMS > 0 {
			time.Sleep(time.Duration(s.opts.ThrottleMS) * time.Millisecond)
		}
	}
}

// writeLoop 批量落库并增量维护内存索引。
//
// 它不会提前返回：results 必须被排空，否则上游 goroutine 会永久阻塞在发送上。
// 出错时记下首个错误并停止后续写入，等 channel 关闭后统一返回。
func (s *IndexService) writeLoop(results <-chan indexResult, stats *indexStats) error {
	batch := make([]store.IndexWrite, 0, s.opts.BatchSize)
	var firstErr error

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if firstErr == nil {
			if err := s.db.WriteIndexResults(s.opts.ModelID, batch); err != nil {
				firstErr = err
				log.Printf("write index batch: %v", err)
			} else {
				for _, it := range batch {
					if len(it.Vector) == 0 {
						// 失败记录：库里旧向量已被删除，内存索引同步移除。
						if it.Asset.IndexState == model.IndexFailed {
							s.idx.Remove(it.Asset.ID)
						}
						continue
					}
					s.idx.Upsert(store.EmbeddingRow{
						AssetID: it.Asset.ID,
						Path:    it.Asset.Path,
						Width:   it.Asset.Width,
						Height:  it.Asset.Height,
						Size:    it.Asset.Size,
						MTime:   it.Asset.MTime,
						Dim:     len(it.Vector),
						Vector:  it.Vector,
					})
				}
			}
		}
		batch = batch[:0]
	}

	for r := range results {
		batch = append(batch, r.toWrite())
		stats.done.Add(1)
		if r.err != nil {
			stats.failed.Add(1)
		} else {
			stats.indexed.Add(1)
		}
		if len(batch) >= cap(batch) {
			flush()
		}
	}
	flush()
	return firstErr
}

func (r indexResult) toWrite() store.IndexWrite {
	c := r.task.candidate
	asset := model.MediaAsset{
		Path:          c.virtual,
		Mime:          mimeOf(c.virtual),
		Size:          c.size,
		Width:         r.width,
		Height:        r.height,
		MTime:         c.mtime,
		Bucket:        c.bucket,
		ChangeToken:   r.task.token,
		Source:        r.source,
		LastIndexedAt: time.Now().UnixMilli(),
	}
	if r.err != nil {
		asset.IndexState = model.IndexFailed
		asset.Error = truncateRunes(r.err.Error(), 200)
		return store.IndexWrite{Asset: asset}
	}
	asset.IndexState = model.IndexDone
	return store.IndexWrite{Asset: asset, Vector: r.vec}
}

// loadImage 取图：缩略图优先，缺失时解原图并回写。
func (s *IndexService) loadImage(c indexCandidate) (image.Image, string, error) {
	size := s.engine.ImageSize()
	if s.opts.PreferThumbnail {
		if p, ok := s.thumbs.Lookup(c.abs); ok {
			if img, err := imgcodec.DecodeFile(p, size); err == nil {
				return img, model.SourceThumb, nil
			}
			// 缩略图损坏时退回原图，不算失败。
		}
	}

	img, err := imgcodec.DecodeFile(c.abs, size)
	if err != nil {
		return nil, "", err
	}
	if s.opts.WritebackThumbnail {
		if _, err := s.thumbs.WriteFrom(img, c.abs); err != nil {
			log.Printf("writeback thumbnail %s: %v", c.virtual, err)
		}
	}
	return img, model.SourceOriginal, nil
}

// walk 遍历全部挂载，收集待索引图片。
//
// 返回候选列表与被成功遍历的挂载点名：清理幽灵记录时只处理这些挂载点，
// 避免把「NAS 临时掉线」误判成「文件已删除」。
func (s *IndexService) walk(onFound func(int)) ([]indexCandidate, []string) {
	var out []indexCandidate
	var walked []string
	for _, m := range s.mounts.List() {
		root := s.files.AbsPath(m.Name)
		if root == "" {
			continue
		}
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			log.Printf("skip mount %s: 不可访问", m.Name)
			continue
		}
		walked = append(walked, m.Name)
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // 忽略无权限等局部错误
			}
			if d.IsDir() {
				if p == root {
					return nil
				}
				info, ierr := d.Info()
				if ierr != nil {
					return nil
				}
				for _, skip := range s.skipDirs {
					if os.SameFile(info, skip) {
						return fs.SkipDir
					}
				}
				return nil
			}
			if _, ok := s.exts[strings.ToLower(filepath.Ext(p))]; !ok {
				return nil
			}
			info, ierr := d.Info()
			if ierr != nil || info.Size() <= 0 {
				return nil
			}
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				return nil
			}
			out = append(out, indexCandidate{
				virtual: virtualJoin(m.Name, filepath.ToSlash(rel)),
				abs:     p,
				size:    info.Size(),
				mtime:   info.ModTime().UnixMilli(),
				bucket:  m.Name,
			})
			if onFound != nil && len(out)%500 == 0 {
				onFound(len(out))
			}
			return nil
		})
	}
	return out, walked
}

// changeToken 是变更检测的比对键。
func changeToken(size, mtime int64) string {
	return fmt.Sprintf("%d:%d", size, mtime)
}

func normalizeExtension(ext string) string {
	e := strings.ToLower(strings.TrimSpace(ext))
	if e == "" {
		return ""
	}
	if !strings.HasPrefix(e, ".") {
		e = "." + e
	}
	return e
}

var mimeByExt = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".bmp":  "image/bmp",
	".webp": "image/webp",
	".heic": "image/heic",
	".heif": "image/heif",
}

func mimeOf(path string) string {
	return mimeByExt[strings.ToLower(filepath.Ext(path))]
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
