package ai

import (
	"image"
	"math"

	"github.com/disintegration/imaging"
)

// Preprocess 把任意尺寸的图片转成模型输入张量（CHW、float32、已归一化）。
//
// 关键点：**直接拉伸到 size×size，不是「短边缩放 + 中心裁剪」**。
// 依据是随权重发布的 preprocessor_config.json（do_center_crop=false），
// 以及 design 文档 §7.2 的 A/B 实测。写错不会报错，只会让检索质量悄悄下降，
// 所以这里单独成函数并用 preproc_golden.npz 对拍。
func Preprocess(img image.Image, size int, mean, std [3]float32) []float32 {
	if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
		// CatmullRom 就是 a=-0.5 的三次卷积核，与 PIL BICUBIC 同族
		// （imaging 没有叫 Bicubic 的常量，别按名字找）。
		img = imaging.Resize(img, size, size, imaging.CatmullRom)
	}
	nrgba, ok := img.(*image.NRGBA)
	if !ok {
		nrgba = imaging.Clone(img)
	}

	plane := size * size
	out := make([]float32, 3*plane)
	pix := nrgba.Pix
	stride := nrgba.Stride

	for y := 0; y < size; y++ {
		row := y * stride
		base := y * size
		for x := 0; x < size; x++ {
			i := row + x*4
			idx := base + x
			out[idx] = (float32(pix[i])/255 - mean[0]) / std[0]
			out[plane+idx] = (float32(pix[i+1])/255 - mean[1]) / std[1]
			out[2*plane+idx] = (float32(pix[i+2])/255 - mean[2]) / std[2]
		}
	}
	return out
}

// L2Normalize 原地归一化并返回同一切片。
//
// 模型输出保持原始尺度（见 meta.json 的 normalize 字段），归一化统一在 Go 侧做，
// 避免两侧口径不一致导致余弦分数对不上。
func L2Normalize(vec []float32) []float32 {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	if sum == 0 {
		return vec
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range vec {
		vec[i] *= inv
	}
	return vec
}

// Cosine 计算两个向量的余弦相似度。对已归一化的向量等价于点积。
func Cosine(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}
