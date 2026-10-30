package service

import (
	"sort"
	"strings"
	"sync"

	"github.com/zhangjh/mini-nas/server/internal/store"
)

// VectorIndex 是常驻内存的暴力余弦索引。
//
// 为什么不上 HNSW/FAISS（design §3 D5）：14k × 512 × 4B 只有 28MB，
// 暴力检索 7.2M 次乘加约 15~30ms，交互无感；引入原生依赖换来的收益
// 在 20 万条以下不存在。切换阈值到了再评估。
type VectorIndex struct {
	mu      sync.RWMutex
	dim     int
	entries []vectorEntry
	pos     map[int64]int // assetID -> entries 下标
	loaded  bool
}

type vectorEntry struct {
	id     int64
	path   string
	vec    []float32
	width  int
	height int
	size   int64
	mtime  int64
}

// VectorHit 是一条命中及其分数。
type VectorHit struct {
	AssetID int64
	Path    string
	Score   float64
	Width   int
	Height  int
	Size    int64
	MTime   int64
}

func NewVectorIndex(dim int) *VectorIndex {
	return &VectorIndex{
		dim: dim,
		pos: make(map[int64]int),
	}
}

// Load 用一份全量快照替换索引。
func (v *VectorIndex) Load(rows []store.EmbeddingRow) {
	v.mu.Lock()
	defer v.mu.Unlock()

	v.entries = make([]vectorEntry, 0, len(rows))
	v.pos = make(map[int64]int, len(rows))
	for _, r := range rows {
		if v.dim > 0 && len(r.Vector) != v.dim {
			continue // 维度不符说明是别的模型的产物，跳过
		}
		v.pos[r.AssetID] = len(v.entries)
		v.entries = append(v.entries, vectorEntry{
			id:     r.AssetID,
			path:   r.Path,
			vec:    r.Vector,
			width:  r.Width,
			height: r.Height,
			size:   r.Size,
			mtime:  r.MTime,
		})
	}
	v.loaded = true
}

// Upsert 写入或替换一条向量。
func (v *VectorIndex) Upsert(row store.EmbeddingRow) {
	if v.dim > 0 && len(row.Vector) != v.dim {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()

	entry := vectorEntry{
		id:     row.AssetID,
		path:   row.Path,
		vec:    row.Vector,
		width:  row.Width,
		height: row.Height,
		size:   row.Size,
		mtime:  row.MTime,
	}
	if idx, ok := v.pos[row.AssetID]; ok {
		v.entries[idx] = entry
		return
	}
	v.pos[row.AssetID] = len(v.entries)
	v.entries = append(v.entries, entry)
}

// Remove 删除一条向量（交换删除，O(1)）。
func (v *VectorIndex) Remove(id int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.removeLocked(id)
}

func (v *VectorIndex) removeLocked(id int64) {
	idx, ok := v.pos[id]
	if !ok {
		return
	}
	last := len(v.entries) - 1
	if idx != last {
		v.entries[idx] = v.entries[last]
		v.pos[v.entries[idx].id] = idx
	}
	v.entries = v.entries[:last]
	delete(v.pos, id)
}

// RemovePaths 按虚拟路径批量删除。
func (v *VectorIndex) RemovePaths(paths []string) {
	if len(paths) == 0 {
		return
	}
	set := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		set[p] = struct{}{}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, e := range v.entries {
		if _, ok := set[e.path]; ok {
			v.removeLocked(e.id)
		}
	}
}

// RemoveUnder 删除某个虚拟目录前缀下的全部向量。
func (v *VectorIndex) RemoveUnder(prefix string) {
	if prefix == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	needle := prefix + "/"
	for _, e := range v.entries {
		if e.path == prefix || strings.HasPrefix(e.path, needle) {
			v.removeLocked(e.id)
		}
	}
}

// RenamePath 跟随改名/移动更新路径。
func (v *VectorIndex) RenamePath(oldPath, newPath string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for i := range v.entries {
		if v.entries[i].path == oldPath {
			v.entries[i].path = newPath
		}
	}
}

// Len 返回索引内向量数。
func (v *VectorIndex) Len() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.entries)
}

// Loaded 表示是否已从库中加载过。
func (v *VectorIndex) Loaded() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.loaded
}

// Search 做暴力余弦检索，返回按分数降序的前 limit 条。
//
// 查询向量与库内向量都已 L2 归一化，点积即余弦。
// excludeID 用于「以图搜图」排除自身。
func (v *VectorIndex) Search(q []float32, limit int, minScore float64, excludeID int64) []VectorHit {
	if limit <= 0 {
		limit = 50
	}
	v.mu.RLock()
	defer v.mu.RUnlock()

	hits := make([]VectorHit, 0, 64)
	for _, e := range v.entries {
		if e.id == excludeID || len(e.vec) != len(q) {
			continue
		}
		var dot float64
		for i, x := range q {
			dot += float64(x) * float64(e.vec[i])
		}
		if dot < minScore {
			continue
		}
		hits = append(hits, VectorHit{
			AssetID: e.id,
			Path:    e.path,
			Score:   dot,
			Width:   e.width,
			Height:  e.height,
			Size:    e.size,
			MTime:   e.mtime,
		})
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}
