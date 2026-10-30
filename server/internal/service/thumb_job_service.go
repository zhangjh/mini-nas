package service

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zhangjh/mini-nas/server/internal/imgcodec"
	"github.com/zhangjh/mini-nas/server/internal/model"
	"github.com/zhangjh/mini-nas/server/internal/store"
)

// JobTypeThumbBackfill 是批量补齐缩略图的任务类型。
const JobTypeThumbBackfill = "thumb_backfill"

// sourceTTL 是图片源清单的缓存有效期。
//
// Status() 必须遍历全部挂载才能算出 images/cached，6 万文件的库单次要 8 秒以上。
// 这个接口只服务于状态展示，几十秒的陈旧度无所谓，所以加一层短期缓存。
// 同时复用同一把锁把并发的全量遍历串行化——否则前端多开几个页面就能把
// NAS 机械盘打满。
const sourceTTL = 30 * time.Second

// ThumbJobService 把缩略图批量补齐包装成可观测、可取消的后台任务。
//
// 与 DedupService 一样，同一时刻只允许一个补齐任务在跑；任务进度写进 job 表，
// 进程重启后由 FailStaleJobs 统一收尾。
type ThumbJobService struct {
	db      *store.DB
	mounts  *MountService
	thumbs  *ThumbnailService
	fileSvc *FileService

	workers int
	// prune 为 true 时顺带清理不再对应任何源文件的缓存。
	prune bool

	mu      sync.Mutex
	running map[string]context.CancelFunc

	// 图片源清单的短期缓存，见 sourceTTL。
	srcMu    sync.Mutex
	srcCache []string
	srcAt    time.Time
	srcValid bool
}

func NewThumbJobService(
	db *store.DB,
	mounts *MountService,
	thumbs *ThumbnailService,
	fileSvc *FileService,
	workers int,
	prune bool,
) *ThumbJobService {
	if workers <= 0 {
		workers = 4
	}
	return &ThumbJobService{
		db:      db,
		mounts:  mounts,
		thumbs:  thumbs,
		fileSvc: fileSvc,
		workers: workers,
		prune:   prune,
		running: map[string]context.CancelFunc{},
	}
}

// Start 启动一次补齐任务。已有任务在跑时直接返回该任务。
func (s *ThumbJobService) Start(force bool) (*model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for jobID := range s.running {
		if job, err := s.db.GetJob(jobID); err == nil {
			return job, nil
		}
	}

	job := &model.Job{
		ID:        uuid.NewString(),
		Type:      JobTypeThumbBackfill,
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
		if err := s.run(ctx, job.ID, force); err != nil {
			log.Printf("thumb backfill %s failed: %v", job.ID, err)
			_ = s.db.FailJob(job.ID, err.Error())
		}
	}()

	return job, nil
}

// Cancel 取消指定任务。
func (s *ThumbJobService) Cancel(jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	cancel, ok := s.running[jobID]
	if !ok {
		return false
	}
	cancel()
	return true
}

// ThumbStatus 汇总当前缓存规模，供前端展示。
type ThumbStatus struct {
	Mounts     int   `json:"mounts"`
	Images     int   `json:"images"`
	Cached     int   `json:"cached"`
	Orphans    int   `json:"orphans"`
	CacheBytes int64 `json:"cache_bytes"`
	// OrphanBytes 是不再对应任何源文件的缓存占用。
	//
	// 单独报出来而不是并进 CacheBytes：CacheBytes 的语义是「有效缓存占多少」，
	// 混进孤儿会让「缓存命中率」之类的推算失真；但只报有效部分又会让
	// 磁盘实际占用被低估（历史键格式迁移后就出现过报 12KB、实占 88MB）。
	// 两个数都给出，调用方相加即可得到真实占用。
	OrphanBytes int64      `json:"orphan_bytes"`
	Running     *model.Job `json:"running"`
}

// Status 统计缓存现状。
//
// 源清单走 30 秒缓存，缓存目录只 ReadDir 一次后与源集合求交，
// 不再对每个源文件单独 Stat——大库下这是从 8 秒降到几十毫秒的关键。
func (s *ThumbJobService) Status() ThumbStatus {
	var st ThumbStatus
	srcs := s.sourcesSnapshot()
	st.Mounts = len(s.mounts.List())
	st.Images = len(srcs)

	keep := make(map[string]struct{}, len(srcs))
	for _, p := range srcs {
		keep[filepath.Base(s.thumbs.CachePath(p))] = struct{}{}
	}

	// 缓存目录一次读全，键为文件名、值为大小。
	cached := make(map[string]int64)
	if entries, err := os.ReadDir(s.thumbs.CacheDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			cached[e.Name()] = info.Size()
		}
	}

	for name := range keep {
		if size, ok := cached[name]; ok {
			st.Cached++
			st.CacheBytes += size
		}
	}
	for name, size := range cached {
		if _, ok := keep[name]; !ok {
			st.Orphans++
			st.OrphanBytes += size
		}
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

// sourcesSnapshot 返回缓存的图片源清单，超过 sourceTTL 才重新遍历。
func (s *ThumbJobService) sourcesSnapshot() []string {
	s.srcMu.Lock()
	defer s.srcMu.Unlock()

	if s.srcValid && time.Since(s.srcAt) < sourceTTL {
		return s.srcCache
	}
	srcs := s.collectSources()
	s.srcCache = srcs
	s.srcAt = time.Now()
	s.srcValid = true
	return srcs
}

// PruneResult 是一次孤儿缓存清理的结果。
type PruneResult struct {
	Removed    int   `json:"removed"`
	FreedBytes int64 `json:"freed_bytes"`
	Kept       int   `json:"kept"`
}

// Prune 清理不再对应任何源文件的缩略图缓存，不生成任何新缩略图。
//
// 补齐任务结束时也会顺带清理（受 prune_orphans 配置控制），但那需要先跑完
// 整个补齐流程；换了缓存键格式、或源文件批量删除之后，用户需要的是一个
// 只清不生成的入口。
func (s *ThumbJobService) Prune() (PruneResult, error) {
	srcs := s.collectSources()
	keep := make(map[string]struct{}, len(srcs))
	for _, p := range srcs {
		keep[filepath.Base(s.thumbs.CachePath(p))] = struct{}{}
	}

	removed, freed, err := s.thumbs.PruneOrphans(keep)
	if err != nil {
		return PruneResult{}, err
	}
	// 源清单没变，但缓存目录变了；下次 Status 会重新 ReadDir，无需失效。
	return PruneResult{Removed: removed, FreedBytes: freed, Kept: len(keep)}, nil
}

// collectSources 遍历所有挂载，收集可解码的图片绝对路径。
func (s *ThumbJobService) collectSources() []string {
	var out []string
	for _, m := range s.mounts.List() {
		root := s.fileSvc.AbsPath(m.Name)
		if root == "" {
			continue
		}
		filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			if imgcodec.Supports(p) {
				out = append(out, p)
			}
			return nil
		})
	}
	return out
}

func (s *ThumbJobService) run(ctx context.Context, jobID string, force bool) error {
	_ = s.db.SetJobState(jobID, model.JobRunning, "遍历图片")

	srcs := s.collectSources()
	total := int64(len(srcs))
	_ = s.db.UpdateJobProgress(jobID, 0, total, "开始生成缩略图")

	// 进度上报限频：每 2% 或至少每 50 张写一次库，避免高频 UPDATE。
	step := total / 50
	if step < 1 {
		step = 1
	}
	var last int64

	stats := s.thumbs.Backfill(ctx, srcs, s.workers, force, func(done, n int) {
		if int64(done)-last < step && done != n {
			return
		}
		last = int64(done)
		_ = s.db.UpdateJobProgress(jobID, int64(done), total, "生成缩略图")
	})

	if ctx.Err() != nil {
		_ = s.db.SetJobState(jobID, model.JobCanceled, "已取消")
		return nil
	}

	msg := statsMessage(stats)
	if s.prune {
		keep := make(map[string]struct{}, len(srcs))
		for _, p := range srcs {
			keep[filepath.Base(s.thumbs.CachePath(p))] = struct{}{}
		}
		if n, _, err := s.thumbs.PruneOrphans(keep); err != nil {
			log.Printf("prune thumbnails: %v", err)
		} else if n > 0 {
			msg += "，清理孤儿 " + strconv.Itoa(n)
		}
	}

	_ = s.db.SetJobState(jobID, model.JobDone, msg)
	return nil
}

func statsMessage(st BackfillStats) string {
	return "完成：生成 " + strconv.Itoa(st.Generated) +
		"，跳过 " + strconv.Itoa(st.Skipped) +
		"，失败 " + strconv.Itoa(st.Failed) +
		"，不支持 " + strconv.Itoa(st.Unsupported)
}
