//go:build !cgo

package ai

// 非 cgo 构建的推理后端：一律不可用。
//
// Windows 本地开发默认走这里，保证没有 gcc 也能编译整个服务端；
// 语义检索接口返回 503，其余功能（文件浏览、去重、缩略图）不受影响。
func newBackend(cfg Config, meta Meta) (backend, error) {
	return nil, ErrUnavailable
}
