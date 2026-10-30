package model

import "os"

type FileInfo struct {
	Name    string      `json:"name"`
	Path    string      `json:"path"`
	Size    int64       `json:"size"`
	IsDir   bool        `json:"is_dir"`
	ModTime int64       `json:"mod_time"`
	Mode    os.FileMode `json:"mode"`
}

type FileList struct {
	Items    []FileInfo `json:"items"`
	Total    int        `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Path     string     `json:"path"`
}

type Mount struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	CreatedAt int64  `json:"created_at"`
}

type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
	CreatedAt    int64  `json:"created_at"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	Username  string `json:"username"`
}

type AuthStatus struct {
	HasUsers bool `json:"has_users"`
}

type APIResponse struct {
	OK    bool        `json:"ok"`
	Data  interface{} `json:"data,omitempty"`
	Error string      `json:"error,omitempty"`
}

// ---- 内容指纹与去重 ----

// FileHash 记录某个虚拟路径文件的内容指纹。
type FileHash struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	MTime      int64  `json:"mtime"`
	MD5        string `json:"md5"`
	ComputedAt int64  `json:"computed_at"`
}

// DedupSkip 记录「该虚拟路径的请求内容已被去重，未落盘」。
//
// 上传时若命中已有内容，按配置跳过写入；为了让客户端不必重复上传，
// 这里保留一条虚拟条目，ListTree 会把它一并返回给同步客户端。
type DedupSkip struct {
	Path        string `json:"path"`
	MD5         string `json:"md5"`
	Size        int64  `json:"size"`
	MTime       int64  `json:"mtime"`
	DuplicateOf string `json:"duplicate_of"`
	CreatedAt   int64  `json:"created_at"`
}

// DupGroup 是一组内容完全相同（md5 与 size 均一致）的文件。
type DupGroup struct {
	ID          string   `json:"id"`
	MD5         string   `json:"md5"`
	Size        int64    `json:"size"`
	MemberCount int      `json:"member_count"`
	Reclaimable int64    `json:"reclaimable"`
	CreatedAt   int64    `json:"created_at"`
	Members     []string `json:"members,omitempty"`
}

// JobState 表示后台任务的运行状态。
type JobState string

const (
	JobPending  JobState = "PENDING"
	JobRunning  JobState = "RUNNING"
	JobDone     JobState = "DONE"
	JobFailed   JobState = "FAILED"
	JobCanceled JobState = "CANCELED"
)

// Job 是后台任务（去重扫描、索引构建等）的进度载体。
type Job struct {
	ID         string   `json:"id"`
	Type       string   `json:"type"`
	State      JobState `json:"state"`
	Total      int64    `json:"total"`
	Done       int64    `json:"done"`
	Message    string   `json:"message"`
	Error      string   `json:"error"`
	StartedAt  int64    `json:"started_at"`
	FinishedAt int64    `json:"finished_at"`
}
