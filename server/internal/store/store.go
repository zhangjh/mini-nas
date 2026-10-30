// Package store 提供 mini-nas 的结构化数据持久化。
//
// 早期版本把挂载点、用户等少量数据直接写成 JSON 文件；内容指纹、重复分组、
// 向量索引这类数据有明确的关联查询与分页需求，JSON 文件不再适用，因此这里
// 引入 SQLite。驱动选 modernc.org/sqlite（纯 Go 实现），避免为了数据库再引入
// 一条 CGO 依赖链——CGO 只留给 ONNX Runtime 这一个无法回避的点。
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB 是对 *sql.DB 的薄封装，只负责连接参数与 schema 迁移。
type DB struct {
	*sql.DB
}

// Open 打开（必要时创建）数据库文件并完成迁移。
//
// path 是数据目录，数据库文件固定为 <path>/mini-nas.db。
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	dsn := filepath.Join(path, "mini-nas.db") +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(10000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite 的并发写模型是单写者；限制连接数避免 "database is locked"。
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(2)
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	db := &DB{DB: sqlDB}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// schema 用 CREATE TABLE IF NOT EXISTS 表达，后续字段变更再补 ALTER。
//
// 说明：
//   - file_hash / dedup_skip 的 path 都是「虚拟路径」（mount/rel），
//     与 API 层一致，不存宿主机绝对路径，避免 HOST_ROOT 变化后失效。
//   - media_asset / media_embedding 属于语义检索（P1），这里先建表，
//     保证 schema 一次成型，避免上线后再做迁移。
const schema = `
CREATE TABLE IF NOT EXISTS file_hash (
	path        TEXT PRIMARY KEY,
	size        INTEGER NOT NULL,
	mtime       INTEGER NOT NULL,
	md5         TEXT NOT NULL,
	computed_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_file_hash_md5 ON file_hash(md5, size);

CREATE TABLE IF NOT EXISTS dedup_skip (
	path         TEXT PRIMARY KEY,
	md5          TEXT NOT NULL,
	size         INTEGER NOT NULL,
	mtime        INTEGER NOT NULL,
	duplicate_of TEXT NOT NULL,
	created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_dedup_skip_md5 ON dedup_skip(md5);

CREATE TABLE IF NOT EXISTS job (
	id          TEXT PRIMARY KEY,
	type        TEXT NOT NULL,
	state       TEXT NOT NULL,
	total       INTEGER NOT NULL DEFAULT 0,
	done        INTEGER NOT NULL DEFAULT 0,
	message     TEXT NOT NULL DEFAULT '',
	error       TEXT NOT NULL DEFAULT '',
	started_at  INTEGER NOT NULL,
	finished_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_job_type_started ON job(type, started_at DESC);

CREATE TABLE IF NOT EXISTS dup_group (
	id           TEXT PRIMARY KEY,
	md5          TEXT NOT NULL,
	size         INTEGER NOT NULL,
	member_count INTEGER NOT NULL,
	reclaimable  INTEGER NOT NULL,
	created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_dup_group_reclaim ON dup_group(reclaimable DESC);

CREATE TABLE IF NOT EXISTS dup_member (
	group_id TEXT NOT NULL,
	path     TEXT NOT NULL,
	PRIMARY KEY (group_id, path)
);
CREATE INDEX IF NOT EXISTS idx_dup_member_path ON dup_member(path);

CREATE TABLE IF NOT EXISTS media_asset (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	path            TEXT NOT NULL UNIQUE,
	mime            TEXT NOT NULL DEFAULT '',
	size            INTEGER NOT NULL DEFAULT 0,
	width           INTEGER NOT NULL DEFAULT 0,
	height          INTEGER NOT NULL DEFAULT 0,
	mtime           INTEGER NOT NULL DEFAULT 0,
	bucket          TEXT NOT NULL DEFAULT '',
	index_state     TEXT NOT NULL DEFAULT 'PENDING',
	last_indexed_at INTEGER NOT NULL DEFAULT 0,
	change_token    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_media_asset_state ON media_asset(index_state);
CREATE INDEX IF NOT EXISTS idx_media_asset_bucket ON media_asset(bucket);

CREATE TABLE IF NOT EXISTS media_embedding (
	asset_id   INTEGER NOT NULL REFERENCES media_asset(id) ON DELETE CASCADE,
	model_id   TEXT NOT NULL,
	dim        INTEGER NOT NULL,
	vector     BLOB NOT NULL,
	created_at INTEGER NOT NULL,
	PRIMARY KEY (asset_id, model_id)
);
CREATE INDEX IF NOT EXISTS idx_media_embedding_model ON media_embedding(model_id);
`

func (d *DB) migrate() error {
	if _, err := d.Exec(schema); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	// 已有库不会重建表，新增字段走 ALTER（幂等）。
	if err := d.migrateMedia(); err != nil {
		return err
	}
	return nil
}
