// Package imgcodec 封装图像解码，屏蔽不同解码后端的差异。
//
// 提供两种实现，由构建标签选择：
//
//	!cgo   标准库 image.Decode（纯 Go，可移植，12MP JPEG 约 380ms）
//	cgo    libjpeg-turbo（DCT 降采样）+ libheif（HEIC），12MP JPEG 约 17ms
//
// 调用方只依赖 DecodeFile / Supports，不需要关心具体后端。
package imgcodec

import (
	"image"
	"path/filepath"
	"strings"
)

// ext 返回小写扩展名，含前导点。
func ext(path string) string {
	return strings.ToLower(filepath.Ext(path))
}

// DecodeFile 解码图像文件。
//
// hint 是调用方期望的最大边长。后端可以据此做降采样（例如 JPEG 直接按
// 1/2、1/4、1/8 解码），调用方应把它当作提示而非精确约束——返回值可能
// 比 hint 大，由调用方自行缩放。
func DecodeFile(path string, hint int) (image.Image, error) {
	return decodeFile(path, hint)
}

// Supports 判断该路径的扩展名是否可能被解码。
//
// 返回 true 不代表解码一定成功（文件可能损坏），返回 false 则一定不支持。
func Supports(path string) bool {
	return supports(ext(path))
}
