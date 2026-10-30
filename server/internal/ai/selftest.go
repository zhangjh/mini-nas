package ai

import (
	"bytes"
	"fmt"
	"image/png"
	"log"
	"path/filepath"
)

// goldenTexts 是导出脚本里参与 golden.npz 文本对拍的三条语料，顺序必须一致。
//
// 它同时充当分词器的一致性校验：Go 侧分出的 input_ids 必须与 Python
// BertTokenizer 逐 token 相同（design 文档 R10）。
var goldenTexts = []string{
	"一只猫坐在窗台上",
	"海边日落，橙色的天空",
	"city skyline at night",
}

// SelfTest 用两个夹具对拍，判定推理链路是否正确。
//
// 分工（design §11.3）：
//   - preproc_golden.npz 定位问题：resize/归一化/CHW 排布错了会在这里暴露；
//   - golden.npz 判定通过：ORT 绑定、输入名、shape 传错会在这里暴露。
//
// 只跑模型级的话，预处理错了只表现为余弦偏低，不知道该改哪儿；
// 只跑预处理级的话，绑定错了反而发现不了。
func (e *Engine) SelfTest(modelDir string, threshold float64) (SelfTestReport, error) {
	report := SelfTestReport{Threshold: threshold}

	if err := e.checkPreprocess(modelDir, &report); err != nil {
		return report, err
	}
	if err := e.checkModels(modelDir, &report); err != nil {
		return report, err
	}
	if err := e.checkTokenizer(modelDir); err != nil {
		return report, err
	}

	report.Pass = report.PreprocCos >= threshold &&
		report.ImageCos >= threshold &&
		report.TextCos >= threshold
	return report, nil
}

func (e *Engine) checkPreprocess(modelDir string, report *SelfTestReport) error {
	fixture, err := LoadNPZ(filepath.Join(modelDir, "preproc_golden.npz"))
	if err != nil {
		return fmt.Errorf("加载 preproc_golden.npz: %w", err)
	}
	src, ok := fixture["source_png"]
	if !ok {
		return fmt.Errorf("preproc_golden.npz 缺少 source_png")
	}
	expected, ok := fixture["expected"]
	if !ok {
		return fmt.Errorf("preproc_golden.npz 缺少 expected")
	}

	pngBytes, err := src.Uint8()
	if err != nil {
		return err
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return fmt.Errorf("解码夹具源图: %w", err)
	}
	want, err := expected.Float32()
	if err != nil {
		return err
	}

	got := Preprocess(img, e.meta.ImageSize, e.meta.Mean(), e.meta.Std())
	if len(got) != len(want) {
		return fmt.Errorf("预处理输出长度不符：期望 %d，实际 %d", len(want), len(got))
	}
	report.PreprocCos = Cosine(got, want)
	return nil
}

func (e *Engine) checkModels(modelDir string, report *SelfTestReport) error {
	fixture, err := LoadNPZ(filepath.Join(modelDir, "golden.npz"))
	if err != nil {
		return fmt.Errorf("加载 golden.npz: %w", err)
	}

	pixels, ok := fixture["pixel_values"]
	if !ok {
		return fmt.Errorf("golden.npz 缺少 pixel_values")
	}
	wantImg, ok := fixture["image_embeds"]
	if !ok {
		return fmt.Errorf("golden.npz 缺少 image_embeds")
	}
	in, err := pixels.Float32()
	if err != nil {
		return err
	}
	ref, err := wantImg.Float32()
	if err != nil {
		return err
	}

	e.inferMu.Lock()
	gotImg, err := e.be.Image(in)
	e.inferMu.Unlock()
	if err != nil {
		return fmt.Errorf("视觉塔对拍推理失败: %w", err)
	}
	report.ImageCos = Cosine(gotImg, ref)

	idsArr, ok := fixture["text_input_ids"]
	if !ok {
		return fmt.Errorf("golden.npz 缺少 text_input_ids")
	}
	maskArr, ok := fixture["text_attention_mask"]
	if !ok {
		return fmt.Errorf("golden.npz 缺少 text_attention_mask")
	}
	wantTxt, ok := fixture["text_embeds"]
	if !ok {
		return fmt.Errorf("golden.npz 缺少 text_embeds")
	}

	allIDs, err := idsArr.Int64()
	if err != nil {
		return err
	}
	allMask, err := maskArr.Int64()
	if err != nil {
		return err
	}
	refTxt, err := wantTxt.Float32()
	if err != nil {
		return err
	}

	seq := e.meta.SeqLen
	dim := e.meta.EmbedDim
	rows := len(allIDs) / seq
	if rows == 0 || len(refTxt) < rows*dim {
		return fmt.Errorf("golden.npz 文本样本形状异常")
	}

	minCos := 1.0
	for i := 0; i < rows; i++ {
		ids := allIDs[i*seq : (i+1)*seq]
		mask := allMask[i*seq : (i+1)*seq]
		e.inferMu.Lock()
		got, err := e.be.Text(ids, mask)
		e.inferMu.Unlock()
		if err != nil {
			return fmt.Errorf("文本塔对拍推理失败: %w", err)
		}
		c := Cosine(got, refTxt[i*dim:(i+1)*dim])
		if c < minCos {
			minCos = c
		}
	}
	report.TextCos = minCos
	return nil
}

// checkTokenizer 逐 token 比对 Go 分词器与 Python 的输出。
//
// 分词偏一位，查询向量就整体偏移，检索质量下降但不会报错——所以必须在这里拦住。
func (e *Engine) checkTokenizer(modelDir string) error {
	fixture, err := LoadNPZ(filepath.Join(modelDir, "golden.npz"))
	if err != nil {
		return fmt.Errorf("加载 golden.npz: %w", err)
	}
	idsArr, ok := fixture["text_input_ids"]
	if !ok {
		return nil
	}
	allIDs, err := idsArr.Int64()
	if err != nil {
		return err
	}
	seq := e.meta.SeqLen
	rows := len(allIDs) / seq
	if rows < len(goldenTexts) {
		log.Printf("golden.npz 文本样本仅 %d 条，跳过分词器对拍", rows)
		return nil
	}

	for i, text := range goldenTexts {
		got, _ := e.tok.Encode(text)
		want := allIDs[i*seq : (i+1)*seq]
		for j := range want {
			if got[j] != want[j] {
				return fmt.Errorf("分词结果与 Python 不一致：%q 第 %d 个 token，期望 %d，实际 %d（Go 侧：%v）",
					text, j, want[j], got[j], e.tok.Tokenize(text))
			}
		}
	}
	return nil
}
