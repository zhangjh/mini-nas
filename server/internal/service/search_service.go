package service

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/zhangjh/mini-nas/server/internal/ai"
	"github.com/zhangjh/mini-nas/server/internal/model"
	"github.com/zhangjh/mini-nas/server/internal/store"
)

// SearchOptions 控制检索默认值与分数阈值。
//
// 图文余弦与图图余弦的分布完全不同（design §8.3）：
// 图文匹配通常 0.20~0.35，图图相似 0.85+ 才算同款，所以阈值必须分开配置。
type SearchOptions struct {
	DefaultLimit  int
	TextMinScore  float64
	ImageMinScore float64
}

// SearchResult 是一次检索的返回。
type SearchResult struct {
	Items           []model.SearchHit `json:"items"`
	TookMS          int64             `json:"took_ms"`
	TotalCandidates int               `json:"total_candidates"`
}

// SearchService 负责「查询编码 → 内存检索 → 存在性校验」。
type SearchService struct {
	engine *ai.Engine
	db     *store.DB
	files  *FileService
	idx    *VectorIndex
	opts   SearchOptions

	loadMu sync.Mutex
}

func NewSearchService(engine *ai.Engine, db *store.DB, files *FileService, idx *VectorIndex, opts SearchOptions) *SearchService {
	if opts.DefaultLimit <= 0 {
		opts.DefaultLimit = 50
	}
	return &SearchService{engine: engine, db: db, files: files, idx: idx, opts: opts}
}

// ensureLoaded 惰性加载向量索引。
//
// 放在首次检索而不是启动时，是为了不拖慢启动；索引任务写入时会增量维护，
// 所以只有进程第一次检索（或全量重建后）才需要走这条路。
func (s *SearchService) ensureLoaded() error {
	if s.idx.Loaded() {
		return nil
	}
	s.loadMu.Lock()
	defer s.loadMu.Unlock()
	if s.idx.Loaded() {
		return nil
	}
	rows, err := s.db.LoadEmbeddings(s.engine.ModelID())
	if err != nil {
		return err
	}
	s.idx.Load(rows)
	return nil
}

// Warmup 在后台预加载向量索引。
//
// 不阻塞启动：14k 条约 28MB，读盘在百毫秒级，但库更大时会更久。
// 目的是让进程起来后的第一次检索不必等冷加载——用户点「智能检索」
// 通常就在启动后不久。
func (s *SearchService) Warmup() {
	go func() {
		start := time.Now()
		if err := s.ensureLoaded(); err != nil {
			log.Printf("WARN 向量索引预加载失败: %v", err)
			return
		}
		log.Printf("向量索引已就绪: %d 条，耗时 %dms", s.idx.Len(), time.Since(start).Milliseconds())
	}()
}

// TextSearch 文搜图。
func (s *SearchService) TextSearch(query string, limit int, minScore float64) (SearchResult, error) {
	start := time.Now()
	if query == "" {
		return SearchResult{}, fmt.Errorf("查询不能为空")
	}
	if err := s.ensureLoaded(); err != nil {
		return SearchResult{}, err
	}
	if limit <= 0 {
		limit = s.opts.DefaultLimit
	}
	if minScore <= 0 {
		minScore = s.opts.TextMinScore
	}

	vec, err := s.engine.EncodeText(query)
	if err != nil {
		return SearchResult{}, err
	}

	// 多取一些候选：命中文件可能已被删除，需要留出补位空间。
	hits := s.idx.Search(vec, limit*2+16, minScore, 0)
	return SearchResult{
		Items:           s.materialize(hits, limit),
		TookMS:          time.Since(start).Milliseconds(),
		TotalCandidates: s.idx.Len(),
	}, nil
}

// SimilarByPath 以图搜图。
//
// 源图没有索引时现场编码并落库：用户经常对刚上传的照片「以图搜图」，
// 此时索引任务可能还没跑到它，直接报「未索引」体验很差。
func (s *SearchService) SimilarByPath(path string, limit int, minScore float64) (SearchResult, error) {
	start := time.Now()
	if path == "" {
		return SearchResult{}, fmt.Errorf("路径不能为空")
	}
	if err := s.ensureLoaded(); err != nil {
		return SearchResult{}, err
	}
	if limit <= 0 {
		limit = s.opts.DefaultLimit
	}
	if minScore <= 0 {
		minScore = s.opts.ImageMinScore
	}

	modelID := s.engine.ModelID()
	vec, selfID, err := s.db.GetEmbeddingByPath(modelID, path)
	if errors.Is(err, store.ErrNotFound) {
		vec, selfID, err = s.encodeOnDemand(path)
		if err != nil {
			return SearchResult{}, err
		}
	} else if err != nil {
		return SearchResult{}, err
	}

	hits := s.idx.Search(vec, limit*2+16, minScore, selfID)
	return SearchResult{
		Items:           s.materialize(hits, limit),
		TookMS:          time.Since(start).Milliseconds(),
		TotalCandidates: s.idx.Len(),
	}, nil
}

// encodeOnDemand 现场编码一张未索引的图，并写回库与内存索引。
func (s *SearchService) encodeOnDemand(path string) ([]float32, int64, error) {
	abs := s.files.AbsPath(path)
	if abs == "" {
		return nil, 0, fmt.Errorf("路径无效: %s", path)
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return nil, 0, fmt.Errorf("文件不存在: %s", path)
	}

	vec, err := s.engine.EncodeImageFile(abs, s.engine.ImageSize())
	if err != nil {
		return nil, 0, err
	}

	modelID := s.engine.ModelID()
	asset := model.MediaAsset{
		Path:          path,
		Mime:          mimeOf(path),
		Size:          info.Size(),
		MTime:         info.ModTime().UnixMilli(),
		IndexState:    model.IndexDone,
		ChangeToken:   changeToken(info.Size(), info.ModTime().UnixMilli()),
		Source:        model.SourceOriginal,
		LastIndexedAt: time.Now().UnixMilli(),
	}
	id, err := s.db.UpsertAsset(asset)
	if err != nil {
		return nil, 0, err
	}
	if err := s.db.PutEmbedding(id, modelID, vec); err != nil {
		return nil, 0, err
	}
	s.idx.Upsert(store.EmbeddingRow{
		AssetID: id,
		Path:    path,
		Size:    asset.Size,
		MTime:   asset.MTime,
		Dim:     len(vec),
		Vector:  vec,
	})
	return vec, id, nil
}

// materialize 把向量命中转成对外结果，并顺手剔除已失效的条目。
//
// 索引与文件系统之间没有事务：文件被删掉、目录被移走之后，向量仍然在库里。
// 每次检索校验一次 os.Stat（亚毫秒），比让用户点开 404 更划算。
func (s *SearchService) materialize(hits []VectorHit, limit int) []model.SearchHit {
	items := make([]model.SearchHit, 0, limit)
	var stale []int64

	for _, h := range hits {
		abs := s.files.AbsPath(h.Path)
		if abs == "" {
			stale = append(stale, h.AssetID)
			continue
		}
		if _, err := os.Stat(abs); err != nil {
			stale = append(stale, h.AssetID)
			continue
		}
		items = append(items, model.SearchHit{
			Path:     h.Path,
			Score:    h.Score,
			ThumbURL: ThumbURL(h.Path),
			Width:    h.Width,
			Height:   h.Height,
			Size:     h.Size,
			MTime:    h.MTime,
		})
		if len(items) >= limit {
			break
		}
	}
	for _, id := range stale {
		s.idx.Remove(id)
	}
	return items
}

// ThumbURL 构造缩略图地址。
//
// 鉴权走查询参数（auth 中间件支持 ?token=），因为 <img src> 带不了请求头；
// 客户端拿到后自行拼 token。
func ThumbURL(path string) string {
	return "/api/thumb?path=" + url.QueryEscape(path)
}
