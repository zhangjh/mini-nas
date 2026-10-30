package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zhangjh/mini-nas/server/internal/model"
)

// ErrNotFound 表示查询目标不存在。
var ErrNotFound = errors.New("not found")

// normPath 把虚拟路径统一成 "/" 分隔。
//
// 虚拟路径是跨端契约：客户端传正斜杠，容器内 filepath.Join 也是正斜杠，
// 只有 Windows 开发环境会产生反斜杠。在存储边界统一，避免 LIKE 前缀
// 匹配在两种环境表现不一致。
func normPath(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}

// ---- 内容指纹 ----

// UpsertFileHash 写入或覆盖某个虚拟路径的内容指纹。
func (d *DB) UpsertFileHash(h model.FileHash) error {
	_, err := d.Exec(`
		INSERT INTO file_hash (path, size, mtime, md5, computed_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			size = excluded.size,
			mtime = excluded.mtime,
			md5 = excluded.md5,
			computed_at = excluded.computed_at`,
		normPath(h.Path), h.Size, h.MTime, h.MD5, h.ComputedAt)
	if err != nil {
		return fmt.Errorf("upsert file_hash: %w", err)
	}
	return nil
}

// UpsertFileHashes 批量写入内容指纹（扫描阶段使用，单事务提交）。
func (d *DB) UpsertFileHashes(items []model.FileHash) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO file_hash (path, size, mtime, md5, computed_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			size = excluded.size,
			mtime = excluded.mtime,
			md5 = excluded.md5,
			computed_at = excluded.computed_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, h := range items {
		if _, err := stmt.Exec(normPath(h.Path), h.Size, h.MTime, h.MD5, h.ComputedAt); err != nil {
			return fmt.Errorf("upsert file_hash %s: %w", h.Path, err)
		}
	}
	return tx.Commit()
}

// GetFileHash 按虚拟路径读取指纹。
func (d *DB) GetFileHash(path string) (*model.FileHash, error) {
	var h model.FileHash
	err := d.QueryRow(`
		SELECT path, size, mtime, md5, computed_at FROM file_hash WHERE path = ?`, normPath(path)).
		Scan(&h.Path, &h.Size, &h.MTime, &h.MD5, &h.ComputedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get file_hash: %w", err)
	}
	return &h, nil
}

// FindByMD5 查找内容相同的既有文件，返回其虚拟路径。
//
// excludePath 用于排除调用方自身（重复上传同一个文件到同一路径时，
// 不应把自己判定成自己的副本）。
func (d *DB) FindByMD5(md5 string, size int64, excludePath string) (string, error) {
	var path string
	err := d.QueryRow(`
		SELECT path FROM file_hash
		WHERE md5 = ? AND size = ? AND path <> ?
		ORDER BY computed_at ASC
		LIMIT 1`, md5, size, normPath(excludePath)).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find by md5: %w", err)
	}
	return path, nil
}

// DeleteFileHash 删除某个路径的指纹（文件被删除时调用）。
func (d *DB) DeleteFileHash(path string) error {
	_, err := d.Exec(`DELETE FROM file_hash WHERE path = ?`, normPath(path))
	return err
}

// DeleteFileHashUnder 删除某个虚拟目录前缀下的所有指纹。
func (d *DB) DeleteFileHashUnder(prefix string) error {
	_, err := d.Exec(`DELETE FROM file_hash WHERE path = ? OR path LIKE ? ESCAPE '\'`,
		normPath(prefix), escapeLike(normPath(prefix))+"/%")
	return err
}

// ---- 去重跳过记录 ----

// RecordSkip 记录一次「跳过写入」。
func (d *DB) RecordSkip(s model.DedupSkip) error {
	if s.CreatedAt == 0 {
		s.CreatedAt = time.Now().UnixMilli()
	}
	_, err := d.Exec(`
		INSERT INTO dedup_skip (path, md5, size, mtime, duplicate_of, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			md5 = excluded.md5,
			size = excluded.size,
			mtime = excluded.mtime,
			duplicate_of = excluded.duplicate_of,
			created_at = excluded.created_at`,
		normPath(s.Path), s.MD5, s.Size, s.MTime, normPath(s.DuplicateOf), s.CreatedAt)
	if err != nil {
		return fmt.Errorf("record skip: %w", err)
	}
	return nil
}

// DeleteSkip 删除跳过记录（该路径真正落盘或被删除时调用）。
func (d *DB) DeleteSkip(path string) error {
	_, err := d.Exec(`DELETE FROM dedup_skip WHERE path = ?`, normPath(path))
	return err
}

// GetSkip 读取单条跳过记录。
func (d *DB) GetSkip(path string) (*model.DedupSkip, error) {
	var s model.DedupSkip
	err := d.QueryRow(`
		SELECT path, md5, size, mtime, duplicate_of, created_at
		FROM dedup_skip WHERE path = ?`, normPath(path)).
		Scan(&s.Path, &s.MD5, &s.Size, &s.MTime, &s.DuplicateOf, &s.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get skip: %w", err)
	}
	return &s, nil
}

// ListSkipsUnder 列出某个虚拟目录下的全部跳过记录。
//
// prefix 为空时返回全部。
func (d *DB) ListSkipsUnder(prefix string) ([]model.DedupSkip, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if prefix == "" {
		rows, err = d.Query(`
			SELECT path, md5, size, mtime, duplicate_of, created_at FROM dedup_skip`)
	} else {
		rows, err = d.Query(`
			SELECT path, md5, size, mtime, duplicate_of, created_at FROM dedup_skip
			WHERE path = ? OR path LIKE ? ESCAPE '\'`,
			normPath(prefix), escapeLike(normPath(prefix))+"/%")
	}
	if err != nil {
		return nil, fmt.Errorf("list skips: %w", err)
	}
	defer rows.Close()

	var out []model.DedupSkip
	for rows.Next() {
		var s model.DedupSkip
		if err := rows.Scan(&s.Path, &s.MD5, &s.Size, &s.MTime, &s.DuplicateOf, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SkipStats 汇总跳过记录的数量与节省的空间。
type SkipStats struct {
	Count int64 `json:"count"`
	Bytes int64 `json:"bytes"`
}

// GetSkipStats 统计去重跳过记录。
func (d *DB) GetSkipStats() (SkipStats, error) {
	var s SkipStats
	err := d.QueryRow(`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM dedup_skip`).
		Scan(&s.Count, &s.Bytes)
	return s, err
}

// ---- 重复分组 ----

// ReplaceGroups 用一次扫描的结果整体替换重复分组。
//
// 扫描是快照语义：每次全量扫描后旧结果即失效，因此先清空再写入，
// 整个过程在一个事务内完成，避免前端读到半成品。
func (d *DB) ReplaceGroups(groups []model.DupGroup) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM dup_member`); err != nil {
		return fmt.Errorf("clear dup_member: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM dup_group`); err != nil {
		return fmt.Errorf("clear dup_group: %w", err)
	}

	stmtGroup, err := tx.Prepare(`
		INSERT INTO dup_group (id, md5, size, member_count, reclaimable, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmtGroup.Close()

	stmtMember, err := tx.Prepare(`INSERT INTO dup_member (group_id, path) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmtMember.Close()

	for _, g := range groups {
		if _, err := stmtGroup.Exec(g.ID, g.MD5, g.Size, g.MemberCount, g.Reclaimable, g.CreatedAt); err != nil {
			return fmt.Errorf("insert dup_group: %w", err)
		}
		for _, m := range g.Members {
			if _, err := stmtMember.Exec(g.ID, normPath(m)); err != nil {
				return fmt.Errorf("insert dup_member: %w", err)
			}
		}
	}

	return tx.Commit()
}

// ListGroups 分页列出重复分组，按可释放空间降序。
//
// minReclaim 为 0 时不过滤。
func (d *DB) ListGroups(page, pageSize int, minReclaim int64) ([]model.DupGroup, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}

	var total int
	if err := d.QueryRow(`
		SELECT COUNT(*) FROM dup_group WHERE reclaimable >= ?`, minReclaim).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := d.Query(`
		SELECT id, md5, size, member_count, reclaimable, created_at
		FROM dup_group
		WHERE reclaimable >= ?
		ORDER BY reclaimable DESC, size DESC
		LIMIT ? OFFSET ?`, minReclaim, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var groups []model.DupGroup
	var ids []string
	for rows.Next() {
		var g model.DupGroup
		if err := rows.Scan(&g.ID, &g.MD5, &g.Size, &g.MemberCount, &g.Reclaimable, &g.CreatedAt); err != nil {
			return nil, 0, err
		}
		groups = append(groups, g)
		ids = append(ids, g.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	if len(ids) > 0 {
		members, err := d.groupMembers(ids)
		if err != nil {
			return nil, 0, err
		}
		for i := range groups {
			groups[i].Members = members[groups[i].ID]
		}
	}
	return groups, total, nil
}

func (d *DB) groupMembers(ids []string) (map[string][]string, error) {
	query := `SELECT group_id, path FROM dup_member WHERE group_id IN (` + placeholders(len(ids)) + `) ORDER BY path`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	rows, err := d.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]string, len(ids))
	for rows.Next() {
		var gid, path string
		if err := rows.Scan(&gid, &path); err != nil {
			return nil, err
		}
		out[gid] = append(out[gid], path)
	}
	return out, rows.Err()
}

// GroupStats 汇总重复分组的整体情况。
type GroupStats struct {
	Groups      int64 `json:"groups"`
	Files       int64 `json:"files"`
	Reclaimable int64 `json:"reclaimable"`
	LastScanAt  int64 `json:"last_scan_at"`
}

// GetGroupStats 汇总重复分组统计。
func (d *DB) GetGroupStats() (GroupStats, error) {
	var s GroupStats
	err := d.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(member_count), 0), COALESCE(SUM(reclaimable), 0),
		       COALESCE(MAX(created_at), 0)
		FROM dup_group`).Scan(&s.Groups, &s.Files, &s.Reclaimable, &s.LastScanAt)
	return s, err
}

// RemoveGroupsContaining 删除包含指定路径的所有分组。
//
// 清理动作执行后，受影响的组不再成立，直接移除比留一份过期数据更安全。
func (d *DB) RemoveGroupsContaining(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := make([]any, len(paths))
	for i, p := range paths {
		args[i] = normPath(p)
	}
	query := `DELETE FROM dup_group WHERE id IN (SELECT DISTINCT group_id FROM dup_member WHERE path IN (` +
		placeholders(len(paths)) + `))`

	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(query, args...); err != nil {
		return fmt.Errorf("remove groups: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM dup_member WHERE path IN (`+placeholders(len(paths))+`)`, args...); err != nil {
		return fmt.Errorf("remove members: %w", err)
	}
	return tx.Commit()
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, 0, n*2-1)
	for i := 0; i < n; i++ {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, '?')
	}
	return string(buf)
}

// escapeLike 转义 LIKE 通配符，使路径前缀匹配按字面量生效。
func escapeLike(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '%', '_', '\\':
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(out)
}
