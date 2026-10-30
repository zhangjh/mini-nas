package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zhangjh/mini-nas/server/internal/model"
)

// CreateJob 新建一条后台任务记录。
func (d *DB) CreateJob(j model.Job) (*model.Job, error) {
	if j.ID == "" {
		return nil, errors.New("job id required")
	}
	if j.StartedAt == 0 {
		j.StartedAt = time.Now().UnixMilli()
	}
	if j.State == "" {
		j.State = model.JobPending
	}
	_, err := d.Exec(`
		INSERT INTO job (id, type, state, total, done, message, error, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.Type, string(j.State), j.Total, j.Done, j.Message, j.Error, j.StartedAt, j.FinishedAt)
	if err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	return &j, nil
}

// GetJob 读取任务当前状态。
func (d *DB) GetJob(id string) (*model.Job, error) {
	var j model.Job
	var state string
	err := d.QueryRow(`
		SELECT id, type, state, total, done, message, error, started_at, finished_at
		FROM job WHERE id = ?`, id).
		Scan(&j.ID, &j.Type, &state, &j.Total, &j.Done, &j.Message, &j.Error, &j.StartedAt, &j.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	j.State = model.JobState(state)
	return &j, nil
}

// UpdateJobProgress 更新进度。高频调用，只写变化字段。
func (d *DB) UpdateJobProgress(id string, done, total int64, message string) error {
	_, err := d.Exec(`
		UPDATE job SET done = ?, total = ?, message = ? WHERE id = ?`,
		done, total, message, id)
	return err
}

// SetJobState 更新任务状态。
func (d *DB) SetJobState(id string, state model.JobState, message string) error {
	finished := int64(0)
	if state == model.JobDone || state == model.JobFailed || state == model.JobCanceled {
		finished = time.Now().UnixMilli()
	}
	_, err := d.Exec(`
		UPDATE job SET state = ?, message = ?, finished_at = ? WHERE id = ?`,
		string(state), message, finished, id)
	return err
}

// FailJob 标记任务失败并记录错误。
func (d *DB) FailJob(id string, errMsg string) error {
	_, err := d.Exec(`
		UPDATE job SET state = ?, error = ?, finished_at = ? WHERE id = ?`,
		string(model.JobFailed), errMsg, time.Now().UnixMilli(), id)
	return err
}

// ListJobs 列出指定类型的最近任务。
func (d *DB) ListJobs(jobType string, limit int) ([]model.Job, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := d.Query(`
		SELECT id, type, state, total, done, message, error, started_at, finished_at
		FROM job WHERE type = ? ORDER BY started_at DESC LIMIT ?`, jobType, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Job
	for rows.Next() {
		var j model.Job
		var state string
		if err := rows.Scan(&j.ID, &j.Type, &state, &j.Total, &j.Done, &j.Message, &j.Error, &j.StartedAt, &j.FinishedAt); err != nil {
			return nil, err
		}
		j.State = model.JobState(state)
		out = append(out, j)
	}
	return out, rows.Err()
}

// FailStaleJobs 把进程重启前遗留的 RUNNING 任务标记为失败。
//
// 后台任务不跨进程恢复；启动时清理可避免前端一直看到「运行中」。
func (d *DB) FailStaleJobs(reason string) (int64, error) {
	res, err := d.Exec(`
		UPDATE job SET state = ?, error = ?, finished_at = ?
		WHERE state IN (?, ?)`,
		string(model.JobFailed), reason, time.Now().UnixMilli(),
		string(model.JobRunning), string(model.JobPending))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
