// Package imgcodec 封装图像解码，屏蔽不同解码后端的差异。
//
// 提供两种实现，由构建标签选择：
//
//	默认（!cgo 或 !libjpeg）   标准库 image.Decode，纯 Go，12MP JPEG 约 380ms
//	cgo + libjpeg              libjpeg-turbo（DCT 降采样），12MP JPEG 约 17ms
//
// 调用方只依赖 DecodeFile / Supports，不需要关心具体后端。
package imgcodec

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	// 注册解码器。webp/bmp 来自 golang.org/x/image，标准库只自带 jpeg/png/gif。
	// 这两个导入必须放在无构建约束的文件里，否则 cgo 构建会缺解码器。
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
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

// stdExts 是当前支持的扩展名。
//
// 不含 HEIC：Go 生态没有可用的纯 Go HEIF 解码器，libheif 绑定也尚未实现。
// 配置里保留 .heic/.heif 是为了将来换后端时不用改配置，但索引会把它当作
// 不支持的格式跳过，而不是记成失败。
var stdExts = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
	".bmp":  true,
	".webp": true,
}

func supports(e string) bool { return stdExts[e] }

// decodeWithStd 用标准库解码。
//
// hint 在此实现中被忽略：image.Decode 总是全分辨率解码，没有降采样能力。
// 这正是 libjpeg 后端存在的理由——纯 Go 解 12MP JPEG 约 380ms，而
// libjpeg-turbo 用 DCT 降采样只要约 17ms。
func decodeWithStd(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return img, nil
}
