//go:build !cgo

package imgcodec

import (
	"fmt"
	"image"
	"os"

	// 注册解码器。webp/bmp 来自 golang.org/x/image，标准库只自带 jpeg/png/gif。
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

// stdExts 是标准库后端支持的扩展名。
//
// 不含 HEIC：Go 生态没有可用的纯 Go HEIF 解码器，需要 cgo 构建启用 libheif。
var stdExts = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
	".bmp":  true,
	".webp": true,
}

func supports(e string) bool { return stdExts[e] }

// decodeFile 用标准库解码。
//
// hint 在此实现中被忽略：image.Decode 总是全分辨率解码，没有降采样能力。
// 这正是 cgo 后端存在的理由——纯 Go 解 12MP JPEG 约 380ms，而 libjpeg-turbo
// 用 DCT 降采样只要约 17ms。
func decodeFile(path string, hint int) (image.Image, error) {
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
