package store

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/zhangjh/mini-nas/server/internal/model"
)

// migrateMedia 给 media_asset 补 P3 需要的两个字段。
//
// schema 用 CREATE TABLE IF NOT EXISTS 建表，已有库不会重建，新增字段只能走
// ALTER；重复执行必须幂等，所以先读 PRAGMA table_info。
func (d *DB) migrateMedia() error {
	existing, err := d.columns("media_asset")
	if err != nil {
		return err
	}
	additions := []struct{ name, decl string }{
		{"source", "TEXT NOT NULL DEFAULT ''"},
		{"error", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, col := range additions {
		if existing[col.name] {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE media_asset ADD COLUMN %s %s", col.name, col.decl)
		if _, err := d.Exec(stmt); err != nil {
			return fmt.Errorf("add column media_asset.%s: %w", col.name, err)
		}
	}
	return nil
}

func (d *DB) columns(table string) (map[string]bool, error) {
	rows, err := d.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, fmt.Errorf("read table_info(%s): %w", table, err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// AssetState 是变更检测需要的资产快照。
type AssetState struct {
	ID          int64
	ChangeToken string
	State       string
	HasVector   bool
}

// AssetSnapshot 一次读出全部资产的索引状态，供扫描阶段做变更检测。
//
// 全量读进内存而不是逐文件查库：14k 条只有几 MB，逐条查询反而要 14k 次往返。
func (d *DB) AssetSnapshot(modelID string) (map[string]AssetState, error) {
	rows, err := d.Query(`
		SELECT a.id, a.path, a.change_token, a.index_state,
		       CASE WHEN e.asset_id IS NULL THEN 0 ELSE 1 END
		FROM media_asset a
		LEFT JOIN media_embedding e ON e.asset_id = a.id AND e.model_id = ?`, modelID)
	if err != nil {
		return nil, fmt.Errorf("asset snapshot: %w", err)
	}
	defer rows.Close()

	out := make(map[string]AssetState)
	for rows.Next() {
		var (
			st   AssetState
			path string
			has  int
		)
		if err := rows.Scan(&st.ID, &path, &st.ChangeToken, &st.State, &has); err != nil {
			return nil, err
		}
		st.HasVector = has == 1
		out[path] = st
	}
	return out, rows.Err()
}

// IndexWrite 是一次索引写入：资产元信息 + 向量（失败时向量为 nil）。
type IndexWrite struct {
	Asset  model.MediaAsset
	Vector []float32
}

// WriteIndexResults 批量写入索引结果（单事务）。
//
// 批量提交的原因不是吞吐，而是 SQLite 的 fsync：逐条写会让 14k 张图的索引
// 变成 14k 次同步落盘，时间全花在 IO 上。
func (d *DB) WriteIndexResults(modelID string, items []IndexWrite) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UnixMilli()
	for i := range items {
		asset := &items[i].Asset
		if asset.LastIndexedAt == 0 {
			asset.LastIndexedAt = now
		}
		id, err := upsertAssetTx(tx, asset)
		if err != nil {
			return err
		}
		asset.ID = id
		if len(items[i].Vector) == 0 {
			// 本次没算出向量：失败时要删掉旧向量，否则检索仍会命中
			// 「内容已经变了但向量还是旧的」这条记录。
			if asset.IndexState == model.IndexFailed {
				if _, err := tx.Exec(`DELETE FROM media_embedding WHERE asset_id = ? AND model_id = ?`,
					id, modelID); err != nil {
					return fmt.Errorf("drop stale embedding asset=%d: %w", id, err)
				}
			}
			continue
		}
		if err := putEmbeddingTx(tx, id, modelID, items[i].Vector); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UpsertAsset 写入或更新一条资产元信息，返回资产 id。
func (d *DB) UpsertAsset(a model.MediaAsset) (int64, error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	id, err := upsertAssetTx(tx, &a)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// PutEmbedding 写入单个向量。
func (d *DB) PutEmbedding(assetID int64, modelID string, vec []float32) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := putEmbeddingTx(tx, assetID, modelID, vec); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertAssetTx(tx *sql.Tx, a *model.MediaAsset) (int64, error) {
	path := normPath(a.Path)
	_, err := tx.Exec(`
		INSERT INTO media_asset
			(path, mime, size, width, height, mtime, bucket, index_state,
			 last_indexed_at, change_token, source, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			mime = excluded.mime,
			size = excluded.size,
			width = excluded.width,
			height = excluded.height,
			mtime = excluded.mtime,
			bucket = excluded.bucket,
			index_state = excluded.index_state,
			last_indexed_at = excluded.last_indexed_at,
			change_token = excluded.change_token,
			source = excluded.source,
			error = excluded.error`,
		path, a.Mime, a.Size, a.Width, a.Height, a.MTime, a.Bucket,
		a.IndexState, a.LastIndexedAt, a.ChangeToken, a.Source, a.Error)
	if err != nil {
		return 0, fmt.Errorf("upsert media_asset %s: %w", path, err)
	}

	var id int64
	if err := tx.QueryRow(`SELECT id FROM media_asset WHERE path = ?`, path).Scan(&id); err != nil {
		return 0, fmt.Errorf("read media_asset id %s: %w", path, err)
	}
	return id, nil
}

func putEmbeddingTx(tx *sql.Tx, assetID int64, modelID string, vec []float32) error {
	_, err := tx.Exec(`
		INSERT INTO media_embedding (asset_id, model_id, dim, vector, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(asset_id, model_id) DO UPDATE SET
			dim = excluded.dim,
			vector = excluded.vector,
			created_at = excluded.created_at`,
		assetID, modelID, len(vec), encodeVector(vec), time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("put embedding asset=%d: %w", assetID, err)
	}
	return nil
}

// GetAssetByPath 按虚拟路径读取资产元信息。
func (d *DB) GetAssetByPath(path string) (*model.MediaAsset, error) {
	var a model.MediaAsset
	err := d.QueryRow(`
		SELECT id, path, mime, size, width, height, mtime, bucket,
		       index_state, last_indexed_at, change_token, source, error
		FROM media_asset WHERE path = ?`, normPath(path)).
		Scan(&a.ID, &a.Path, &a.Mime, &a.Size, &a.Width, &a.Height, &a.MTime, &a.Bucket,
			&a.IndexState, &a.LastIndexedAt, &a.ChangeToken, &a.Source, &a.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get media_asset: %w", err)
	}
	return &a, nil
}

// EmbeddingRow 是一条已索引的向量及其资产元信息。
type EmbeddingRow struct {
	AssetID int64
	Path    string
	Width   int
	Height  int
	Size    int64
	MTime   int64
	Dim     int
	Vector  []float32
}

// LoadEmbeddings 读取某个模型的全部向量，供内存索引构建。
func (d *DB) LoadEmbeddings(modelID string) ([]EmbeddingRow, error) {
	rows, err := d.Query(`
		SELECT e.asset_id, a.path, a.width, a.height, a.size, a.mtime, e.dim, e.vector
		FROM media_embedding e
		JOIN media_asset a ON a.id = e.asset_id
		WHERE e.model_id = ?
		ORDER BY e.asset_id`, modelID)
	if err != nil {
		return nil, fmt.Errorf("load embeddings: %w", err)
	}
	defer rows.Close()

	var out []EmbeddingRow
	for rows.Next() {
		var (
			r    EmbeddingRow
			blob []byte
		)
		if err := rows.Scan(&r.AssetID, &r.Path, &r.Width, &r.Height, &r.Size, &r.MTime, &r.Dim, &blob); err != nil {
			return nil, err
		}
		vec, err := decodeVector(blob)
		if err != nil {
			return nil, fmt.Errorf("decode vector asset=%d: %w", r.AssetID, err)
		}
		r.Vector = vec
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetEmbeddingByPath 按虚拟路径取向量。
func (d *DB) GetEmbeddingByPath(modelID, path string) ([]float32, error) {
	var blob []byte
	err := d.QueryRow(`
		SELECT e.vector FROM media_embedding e
		JOIN media_asset a ON a.id = e.asset_id
		WHERE e.model_id = ? AND a.path = ?`, modelID, normPath(path)).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get embedding: %w", err)
	}
	return decodeVector(blob)
}

// IndexCounts 是索引覆盖率统计。
type IndexCounts struct {
	Total   int64 `json:"total"`
	Indexed int64 `json:"indexed"`
	Pending int64 `json:"pending"`
	Failed  int64 `json:"failed"`
}

// IndexCounts 统计某个模型的索引覆盖情况。
func (d *DB) IndexCounts(modelID string) (IndexCounts, error) {
	var c IndexCounts
	if err := d.QueryRow(`SELECT COUNT(*) FROM media_asset`).Scan(&c.Total); err != nil {
		return c, err
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM media_embedding WHERE model_id = ?`, modelID).Scan(&c.Indexed); err != nil {
		return c, err
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM media_asset WHERE index_state = ?`, model.IndexFailed).Scan(&c.Failed); err != nil {
		return c, err
	}
	// pending 排除 FAILED：失败资产在下次增量扫描时会被重试，但不应和
	// 「待索引」混在一起统计，否则 total != indexed + pending + failed。
	err := d.QueryRow(`
		SELECT COUNT(*) FROM media_asset a
		LEFT JOIN media_embedding e ON e.asset_id = a.id AND e.model_id = ?
		WHERE e.asset_id IS NULL AND a.index_state <> ?`, modelID, model.IndexFailed).Scan(&c.Pending)
	return c, err
}

// DeleteAssets 按虚拟路径删除资产（向量随外键级联删除）。
func (d *DB) DeleteAssets(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	const batch = 400
	for start := 0; start < len(paths); start += batch {
		end := start + batch
		if end > len(paths) {
			end = len(paths)
		}
		chunk := paths[start:end]
		args := make([]any, len(chunk))
		for i, p := range chunk {
			args[i] = normPath(p)
		}
		query := `DELETE FROM media_asset WHERE path IN (` + placeholders(len(chunk)) + `)`
		if _, err := d.Exec(query, args...); err != nil {
			return fmt.Errorf("delete media_asset: %w", err)
		}
	}
	return nil
}

// DeleteAssetsUnder 删除某个虚拟目录前缀下的资产。
func (d *DB) DeleteAssetsUnder(prefix string) error {
	_, err := d.Exec(`DELETE FROM media_asset WHERE path = ? OR path LIKE ? ESCAPE '\'`,
		normPath(prefix), escapeLike(normPath(prefix))+"/%")
	return err
}

// RenameAsset 跟随改名/移动更新虚拟路径。
//
// 目标路径已存在时忽略冲突：说明索引里已有一条更新过的记录，
// 让下一次扫描去收敛，比在这里做合并更安全。
func (d *DB) RenameAsset(oldPath, newPath string) error {
	_, err := d.Exec(`UPDATE OR IGNORE media_asset SET path = ? WHERE path = ?`,
		normPath(newPath), normPath(oldPath))
	return err
}

// PruneAssets 删除不在 keep 集合中的资产，返回被删除的资产 id。
//
// 源文件被删除或改名后，索引里的记录会变成「幽灵」：检索能命中，
// 打开却是 404。扫描结束时顺手清掉，比让用户自己发现更可靠。
//
// scope 限定要清理的挂载点名。挂载点临时不可用（NAS 掉线）时它的子树
// 根本没被遍历，若不加范围就会把整棵子树的向量误删。
func (d *DB) PruneAssets(keep map[string]struct{}, scope []string) ([]int64, error) {
	if len(scope) == 0 {
		return nil, nil
	}
	rows, err := d.Query(`SELECT id, path FROM media_asset`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var (
		staleIDs   []int64
		stalePaths []string
	)
	for rows.Next() {
		var (
			id   int64
			path string
		)
		if err := rows.Scan(&id, &path); err != nil {
			return nil, err
		}
		if !inScope(path, scope) {
			continue
		}
		if _, ok := keep[path]; ok {
			continue
		}
		staleIDs = append(staleIDs, id)
		stalePaths = append(stalePaths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(stalePaths) == 0 {
		return nil, nil
	}
	if err := d.DeleteAssets(stalePaths); err != nil {
		return nil, err
	}
	return staleIDs, nil
}

// inScope 判断虚拟路径是否属于给定挂载点之一。
func inScope(path string, scope []string) bool {
	for _, name := range scope {
		if name == "" {
			continue
		}
		if path == name || strings.HasPrefix(path, name+"/") {
			return true
		}
	}
	return false
}

// encodeVector 把向量编码为 float32 小端 BLOB。
func encodeVector(vec []float32) []byte {
	buf := make([]byte, 4*len(vec))
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

func decodeVector(blob []byte) ([]float32, error) {
	if len(blob)%4 != 0 {
		return nil, fmt.Errorf("向量字节长度 %d 不是 4 的倍数", len(blob))
	}
	out := make([]float32, len(blob)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return out, nil
}
