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

// Status 汇总当前缓存规模，供前端展示。
type ThumbStatus struct {
	Mounts     int        `json:"mounts"`
	Images     int        `json:"images"`
	Cached     int        `json:"cached"`
	Orphans    int        `json:"orphans"`
	CacheBytes int64      `json:"cache_bytes"`
	Running    *model.Job `json:"running"`
}

// Status 统计缓存现状。会遍历全部挂载，大库下有一定开销，仅供手动刷新。
func (s *ThumbJobService) Status() ThumbStatus {
	var st ThumbStatus
	srcs := s.collectSources()
	st.Mounts = len(s.mounts.List())
	st.Images = len(srcs)

	keep := make(map[string]struct{}, len(srcs))
	for _, p := range srcs {
		dst := s.thumbs.CachePath(p)
		keep[filepath.Base(dst)] = struct{}{}
		if info, err := os.Stat(dst); err == nil {
			st.Cached++
			st.CacheBytes += info.Size()
		}
	}

	if entries, err := os.ReadDir(s.thumbs.CacheDir); err == nil {
		for _, e := range entries {
			if _, ok := keep[e.Name()]; !ok && !e.IsDir() {
				st.Orphans++
			}
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
		if n, err := s.thumbs.PruneOrphans(keep); err != nil {
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
