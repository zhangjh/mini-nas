package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/zhangjh/mini-nas/server/internal/model"
	"github.com/zhangjh/mini-nas/server/internal/store"
)

// hashFlushBatch 是扫描阶段批量写入指纹表的条数。
const hashFlushBatch = 500

// DedupOptions 控制重复扫描的行为。
type DedupOptions struct {
	// LargeFileThreshold 超过该大小的文件先做部分哈希预筛。
	LargeFileThreshold int64
	// PartialHashBytes 部分哈希读取的字节数。
	PartialHashBytes int64
	// MaxScanFiles 单次扫描的文件数量上限，超出则中止，避免拖垮设备。
	MaxScanFiles int
	// HashWorkers 并发哈希协程数。NAS 多为机械盘，2~4 比较合适。
	HashWorkers int
	// ExcludePaths 不参与扫描的绝对路径前缀（服务端自身数据目录等）。
	ExcludePaths []string
}

// DefaultDedupOptions 返回一组保守的默认值。
func DefaultDedupOptions() DedupOptions {
	return DedupOptions{
		LargeFileThreshold: 50 * 1024 * 1024,
		PartialHashBytes:   64 * 1024,
		MaxScanFiles:       500000,
		HashWorkers:        4,
	}
}

// ResolveMode 指定重复文件的处理方式。
type ResolveMode string

const (
	// ResolveHardlink 用硬链接合并：多个路径指向同一份数据，磁盘只占一份。
	ResolveHardlink ResolveMode = "hardlink"
	// ResolveDelete 把冗余副本移入回收站，可恢复。
	ResolveDelete ResolveMode = "delete"
)

// ResolveRequest 是一次清理请求。
type ResolveRequest struct {
	Keep    string      `json:"keep"`
	Remove  []string    `json:"remove"`
	Mode    ResolveMode `json:"mode"`
	Confirm bool        `json:"confirm"`
}

// ResolveFailure 记录单个文件处理失败的原因。
type ResolveFailure struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// ResolveResult 是清理结果。
type ResolveResult struct {
	Mode       ResolveMode      `json:"mode"`
	Merged     []string         `json:"merged"`
	Failed     []ResolveFailure `json:"failed"`
	FreedBytes int64            `json:"freed_bytes"`
}

// DedupService 负责内容去重的两条链路：
//
//   - 存量扫描：全库遍历，按「大小 → 部分哈希 → 全量哈希」三级收敛出重复组；
//   - 清理执行：把冗余副本合并为硬链接，或移入回收站。
//
// 上传路径的实时去重不在这里，由 HashService 直接支撑（见 api.Upload）。
type DedupService struct {
	db       *store.DB
	mountSvc *MountService
	hashSvc  *HashService
	opts     DedupOptions
	trashDir string

	mu      sync.Mutex
	running map[string]context.CancelFunc // jobID -> cancel
}

func NewDedupService(db *store.DB, mountSvc *MountService, hashSvc *HashService, opts DedupOptions, trashDir string) *DedupService {
	if opts.HashWorkers <= 0 {
		opts.HashWorkers = 4
	}
	os.MkdirAll(trashDir, 0o755)
	return &DedupService{
		db:       db,
		mountSvc: mountSvc,
		hashSvc:  hashSvc,
		opts:     opts,
		trashDir: trashDir,
		running:  make(map[string]context.CancelFunc),
	}
}

// scanEntry 是扫描阶段收集到的候选文件。
type scanEntry struct {
	virtual string
	abs     string
	size    int64
	mtime   int64
}

// StartScan 启动一次存量扫描。root 为空表示扫描全部挂载点。
//
// 同一时间只允许一个扫描任务：并发扫描会互相覆盖分组结果，且会把磁盘 IO 打满。
// 若已有任务在跑，直接返回该任务，调用方无需区分「新建」与「复用」。
func (s *DedupService) StartScan(root string) (*model.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for jobID := range s.running {
		if job, err := s.db.GetJob(jobID); err == nil {
			return job, nil
		}
	}

	job := &model.Job{
		ID:        uuid.NewString(),
		Type:      "dedup_scan",
		State:     model.JobPending,
		Message:   "准备扫描",
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
		if err := s.runScan(ctx, job.ID, root); err != nil {
			log.Printf("dedup scan %s failed: %v", job.ID, err)
			_ = s.db.FailJob(job.ID, err.Error())
		}
	}()

	return job, nil
}

// CancelScan 取消正在运行的扫描任务。
func (s *DedupService) CancelScan(jobID string) bool {
	s.mu.Lock()
	cancel, ok := s.running[jobID]
	s.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// GetJob 读取扫描任务状态。
func (s *DedupService) GetJob(jobID string) (*model.Job, error) {
	return s.db.GetJob(jobID)
}

// ListJobs 列出最近的扫描任务。
func (s *DedupService) ListJobs(limit int) ([]model.Job, error) {
	return s.db.ListJobs("dedup_scan", limit)
}

// Stats 汇总重复分组与去重跳过的情况。
//
// 跳过统计走 VerifySkipTarget 校验后的口径：原件已被删除的悬空记录不计入，
// 否则「已节省字节」会在原件删掉之后永久虚高。
func (s *DedupService) Stats() (store.GroupStats, store.SkipStats, error) {
	g, err := s.db.GetGroupStats()
	if err != nil {
		return g, store.SkipStats{}, err
	}
	if s.hashSvc == nil {
		return g, store.SkipStats{}, nil
	}
	k, err := s.hashSvc.SkipStatsVerified()
	return g, k, err
}

// ListGroups 分页读取重复分组。
func (s *DedupService) ListGroups(page, pageSize int, minReclaim int64) ([]model.DupGroup, int, error) {
	return s.db.ListGroups(page, pageSize, minReclaim)
}

// runScan 是扫描主流程。
func (s *DedupService) runScan(ctx context.Context, jobID, root string) error {
	if err := s.db.SetJobState(jobID, model.JobRunning, "扫描目录"); err != nil {
		return err
	}

	entries, err := s.collect(root, func(n int) {
		_ = s.db.UpdateJobProgress(jobID, int64(n), 0, fmt.Sprintf("已发现 %d 个文件", n))
	})
	if err != nil {
		return err
	}
	if len(entries) > s.opts.MaxScanFiles {
		return fmt.Errorf("文件数量 %d 超过上限 %d，请缩小扫描范围", len(entries), s.opts.MaxScanFiles)
	}

	candidates := groupBySize(entries)
	var total int64
	for _, group := range candidates {
		total += int64(len(group))
	}
	if total == 0 {
		if err := s.db.ReplaceGroups(nil); err != nil {
			return err
		}
		return s.db.SetJobState(jobID, model.JobDone, "未发现大小相同的文件")
	}

	_ = s.db.UpdateJobProgress(jobID, 0, total, "计算内容指纹")

	var (
		done    atomic.Int64
		results []model.DupGroup
		resMu   sync.Mutex
	)

	report := func() {
		_ = s.db.UpdateJobProgress(jobID, done.Load(), total,
			fmt.Sprintf("已计算 %d/%d", done.Load(), total))
	}

	for _, group := range candidates {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		var hashed map[string][]scanEntry
		if group[0].size > s.opts.LargeFileThreshold {
			// 大文件：先按前 N 字节收敛，再对仍重复的做全量哈希。
			partial := s.groupByHash(ctx, group, true, &done, report)
			hashed = make(map[string][]scanEntry)
			for _, sub := range partial {
				if len(sub) < 2 {
					continue
				}
				for k, v := range s.groupByHash(ctx, sub, false, &done, report) {
					hashed[k] = v
				}
			}
		} else {
			hashed = s.groupByHash(ctx, group, false, &done, report)
		}

		for md5sum, members := range hashed {
			if len(members) < 2 {
				continue
			}
			paths := make([]string, 0, len(members))
			for _, m := range members {
				paths = append(paths, m.virtual)
			}
			sort.Strings(paths)

			resMu.Lock()
			results = append(results, model.DupGroup{
				ID:          uuid.NewString(),
				MD5:         md5sum,
				Size:        members[0].size,
				MemberCount: len(members),
				Reclaimable: members[0].size * int64(len(members)-1),
				CreatedAt:   time.Now().UnixMilli(),
				Members:     paths,
			})
			resMu.Unlock()
		}
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Reclaimable > results[j].Reclaimable
	})
	if err := s.db.ReplaceGroups(results); err != nil {
		return err
	}

	// 扫描顺带刷新了指纹表（见 groupByHash），上传去重可直接复用。
	_ = s.db.UpdateJobProgress(jobID, total, total,
		fmt.Sprintf("完成，发现 %d 组重复", len(results)))
	return s.db.SetJobState(jobID, model.JobDone,
		fmt.Sprintf("扫描完成：%d 组重复，可释放 %s", len(results), humanBytes(sumReclaimable(results))))
}

// collect 遍历目标目录，返回大小大于 0 的候选文件。
//
// 服务端自身的缓存目录（缩略图、回收站、模型）必须排除，否则会把
// 自己产生的文件也算进重复统计。
func (s *DedupService) collect(root string, onProgress func(int)) ([]scanEntry, error) {
	roots, err := s.scanRoots(root)
	if err != nil {
		return nil, err
	}

	var (
		entries []scanEntry
		mu      sync.Mutex
	)

	for _, r := range roots {
		walkErr := filepath.WalkDir(r.abs, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // 忽略无权限等局部错误
			}
			if s.excluded(p) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}

			info, err := d.Info()
			if err != nil || info.Size() <= 0 {
				return nil
			}

			rel, err := filepath.Rel(r.abs, p)
			if err != nil {
				return nil
			}

			mu.Lock()
			entries = append(entries, scanEntry{
				virtual: filepath.ToSlash(filepath.Join(r.name, rel)),
				abs:     p,
				size:    info.Size(),
				mtime:   info.ModTime().UnixMilli(),
			})
			n := len(entries)
			mu.Unlock()

			if onProgress != nil && n%500 == 0 {
				onProgress(n)
			}
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	return entries, nil
}

type scanRoot struct {
	name string
	abs  string
}

func (s *DedupService) scanRoots(root string) ([]scanRoot, error) {
	if root == "" || root == "." {
		mounts := s.mountSvc.List()
		if len(mounts) == 0 {
			return nil, fmt.Errorf("没有可扫描的挂载点")
		}
		out := make([]scanRoot, 0, len(mounts))
		for _, m := range mounts {
			abs := s.hashSvc.AbsPathOf(m.Name)
			if abs == "" {
				continue
			}
			if info, err := os.Stat(abs); err != nil || !info.IsDir() {
				continue
			}
			out = append(out, scanRoot{name: m.Name, abs: abs})
		}
		return out, nil
	}

	abs := s.hashSvc.AbsPathOf(root)
	if abs == "" {
		return nil, fmt.Errorf("挂载点不存在: %s", root)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("目录不可用: %s", root)
	}

	mount, mountName, _ := s.mountSvc.ResolveMount(root)
	if mount == nil {
		return nil, fmt.Errorf("挂载点不存在: %s", root)
	}
	return []scanRoot{{name: mountName, abs: abs}}, nil
}

func (s *DedupService) excluded(abs string) bool {
	for _, p := range s.opts.ExcludePaths {
		if p == "" {
			continue
		}
		if abs == p || strings.HasPrefix(abs, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// groupByHash 并发计算指纹并按指纹分组。
//
// partial 为 true 时只读取前 PartialHashBytes 字节。全量哈希的结果会顺带
// 写入 file_hash 表——扫描本身已经付出了完整的 IO 代价，不落库就等于让
// 上传去重再算一遍。
func (s *DedupService) groupByHash(ctx context.Context, entries []scanEntry, partial bool, done *atomic.Int64, report func()) map[string][]scanEntry {
	type result struct {
		hash  string
		entry scanEntry
	}

	jobs := make(chan scanEntry)
	results := make(chan result, s.opts.HashWorkers*2)

	var wg sync.WaitGroup
	for i := 0; i < s.opts.HashWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range jobs {
				var (
					sum string
					err error
				)
				if partial {
					sum, err = PartialMD5(e.abs, s.opts.PartialHashBytes)
				} else {
					sum, err = MD5File(e.abs)
				}
				if err != nil {
					log.Printf("hash %s: %v", e.abs, err)
					continue
				}
				results <- result{hash: sum, entry: e}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, e := range entries {
			select {
			case <-ctx.Done():
				return
			case jobs <- e:
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	out := make(map[string][]scanEntry)
	pending := make([]model.FileHash, 0, hashFlushBatch)
	var processed int64

	flush := func() {
		if len(pending) == 0 {
			return
		}
		if err := s.db.UpsertFileHashes(pending); err != nil {
			log.Printf("persist file_hash: %v", err)
		}
		pending = pending[:0]
	}

	for r := range results {
		out[r.hash] = append(out[r.hash], r.entry)

		if !partial {
			pending = append(pending, model.FileHash{
				Path:       r.entry.virtual,
				Size:       r.entry.size,
				MTime:      r.entry.mtime,
				MD5:        r.hash,
				ComputedAt: time.Now().UnixMilli(),
			})
			if len(pending) >= hashFlushBatch {
				flush()
			}
		}

		processed++
		done.Add(1)
		if report != nil && processed%50 == 0 {
			report()
		}
	}
	flush()
	return out
}

// Resolve 执行清理：把 remove 中的文件合并到 keep，或移入回收站。
//
// 安全约束：
//   - 必须显式 confirm；
//   - keep 必须存在且不在 remove 中；
//   - 硬链接先在临时路径建链，成功后再原子替换，避免「先删后链失败」丢数据。
func (s *DedupService) Resolve(req ResolveRequest) (*ResolveResult, error) {
	if !req.Confirm {
		return nil, fmt.Errorf("需要确认后才能执行清理")
	}
	if req.Keep == "" || len(req.Remove) == 0 {
		return nil, fmt.Errorf("必须指定保留文件与待清理文件")
	}
	if req.Mode == "" {
		req.Mode = ResolveHardlink
	}
	if req.Mode != ResolveHardlink && req.Mode != ResolveDelete {
		return nil, fmt.Errorf("不支持的处理方式: %s", req.Mode)
	}

	keepAbs := s.hashSvc.AbsPathOf(req.Keep)
	if keepAbs == "" {
		return nil, fmt.Errorf("保留文件路径无效: %s", req.Keep)
	}
	if info, err := os.Stat(keepAbs); err != nil || info.IsDir() {
		return nil, fmt.Errorf("保留文件不存在: %s", req.Keep)
	}

	result := &ResolveResult{Mode: req.Mode}
	ts := time.Now().Format("20060102-150405")

	for _, p := range req.Remove {
		if p == req.Keep {
			result.Failed = append(result.Failed, ResolveFailure{Path: p, Reason: "不能清理保留文件本身"})
			continue
		}
		abs := s.hashSvc.AbsPathOf(p)
		if abs == "" {
			result.Failed = append(result.Failed, ResolveFailure{Path: p, Reason: "路径无效"})
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			result.Failed = append(result.Failed, ResolveFailure{Path: p, Reason: "文件不存在"})
			continue
		}
		size := info.Size()

		var opErr error
		switch req.Mode {
		case ResolveHardlink:
			opErr = hardlinkOver(keepAbs, abs)
		case ResolveDelete:
			opErr = s.moveToTrash(abs, p, ts)
		}
		if opErr != nil {
			result.Failed = append(result.Failed, ResolveFailure{Path: p, Reason: opErr.Error()})
			continue
		}

		result.Merged = append(result.Merged, p)
		if req.Mode == ResolveDelete {
			result.FreedBytes += size
			_ = s.hashSvc.Forget(p)
		}
		_ = s.hashSvc.ForgetSkip(p)
	}

	if len(result.Merged) > 0 {
		if err := s.db.RemoveGroupsContaining(result.Merged); err != nil {
			log.Printf("remove groups after resolve: %v", err)
		}
	}
	return result, nil
}

// hardlinkOver 让 dst 变成 src 的硬链接。
//
// 先在 dst 同目录建临时链接再 rename 覆盖，保证任何一步失败都不会
// 破坏 dst 原有数据（跨文件系统、文件系统不支持硬链接时会在此失败）。
func hardlinkOver(src, dst string) error {
	tmp := dst + ".mininas-link.tmp"
	_ = os.Remove(tmp)

	if err := os.Link(src, tmp); err != nil {
		return fmt.Errorf("创建硬链接失败（可能跨文件系统或文件系统不支持）: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换文件失败: %w", err)
	}
	return nil
}

// moveToTrash 把文件移入回收站，保留原虚拟路径结构便于恢复。
func (s *DedupService) moveToTrash(abs, virtual, ts string) error {
	dst := filepath.Join(s.trashDir, ts, filepath.FromSlash(virtual))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(abs, dst); err == nil {
		return nil
	}
	// 跨文件系统时退化为复制后删除。
	if err := copyFile(abs, dst); err != nil {
		return err
	}
	return os.Remove(abs)
}

func groupBySize(entries []scanEntry) [][]scanEntry {
	bySize := make(map[int64][]scanEntry, len(entries)/2)
	for _, e := range entries {
		bySize[e.size] = append(bySize[e.size], e)
	}

	out := make([][]scanEntry, 0, len(bySize))
	for _, group := range bySize {
		if len(group) >= 2 {
			out = append(out, group)
		}
	}
	// 大组优先，让用户更早看到收益最大的结果。
	sort.Slice(out, func(i, j int) bool {
		return len(out[i])*int(out[i][0].size) > len(out[j])*int(out[j][0].size)
	})
	return out
}

func sumReclaimable(groups []model.DupGroup) int64 {
	var total int64
	for _, g := range groups {
		total += g.Reclaimable
	}
	return total
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	value := float64(n)
	for _, u := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, u)
		}
	}
	return fmt.Sprintf("%.1f EB", value/unit)
}
