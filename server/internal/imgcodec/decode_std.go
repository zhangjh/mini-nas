//go:build !cgo || !libjpeg

package imgcodec

import "image"

// decodeFile 在非 libjpeg 构建下退化为标准库全解码。
//
// Windows 本地开发（CGO_ENABLED=0）走这里，保证没有 gcc 也能编译；
// 代价是 12MP JPEG 约 380ms，索引吞吐会掉到约 1.9 img/s。
func decodeFile(path string, hint int) (image.Image, error) {
	return decodeWithStd(path)
}
