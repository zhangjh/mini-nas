package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// 模型默认归一化参数。meta.json 缺失这些字段时兜底，避免旧产物不可用。
var (
	defaultMean = [3]float32{0.48145466, 0.4578275, 0.40821073}
	defaultStd  = [3]float32{0.26862954, 0.26130258, 0.27577711}
)

// Meta 是模型目录下 meta.json 的映射，描述输入输出契约。
//
// Go 侧不硬编码维度与输入名：换模型（或重新导出）后只要 meta.json 跟着变，
// 代码无需修改。唯一的硬约束是**静态 shape**，见 design 文档 §2.5。
type Meta struct {
	ModelID    string `json:"model_id"`
	EmbedDim   int    `json:"embed_dim"`
	ImageSize  int    `json:"image_size"`
	SeqLen     int    `json:"seq_len"`
	VocabSize  int    `json:"vocab_size"`
	ImageModel string `json:"image_encoder"`
	TextModel  string `json:"text_encoder"`
	ImageInput string `json:"image_input"`
	// ImageOutput 是视觉塔输出名。
	ImageOutput string   `json:"image_output"`
	TextInputs  []string `json:"text_inputs"`
	TextOutput  string   `json:"text_output"`
	Normalize   string   `json:"normalize"`
	Preprocess  struct {
		Resize   string    `json:"resize"`
		Crop     string    `json:"crop"`
		Size     []int     `json:"size"`
		Resample string    `json:"resample"`
		Mean     []float32 `json:"mean"`
		Std      []float32 `json:"std"`
	} `json:"preprocess"`
}

// LoadMeta 读取并校验 meta.json。
func LoadMeta(modelDir string) (Meta, error) {
	var m Meta
	path := filepath.Join(modelDir, "meta.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return m, fmt.Errorf("读取 meta.json 失败（模型目录 %s）: %w", modelDir, err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("解析 meta.json 失败: %w", err)
	}
	if err := m.validate(); err != nil {
		return m, err
	}
	return m, nil
}

func (m *Meta) validate() error {
	if m.EmbedDim <= 0 {
		return fmt.Errorf("meta.json: embed_dim 非法 (%d)", m.EmbedDim)
	}
	if m.ImageSize <= 0 {
		return fmt.Errorf("meta.json: image_size 非法 (%d)", m.ImageSize)
	}
	if m.SeqLen <= 0 {
		return fmt.Errorf("meta.json: seq_len 非法 (%d)", m.SeqLen)
	}
	if m.ImageInput == "" || m.ImageOutput == "" {
		return fmt.Errorf("meta.json: 缺少视觉塔输入/输出名")
	}
	if len(m.TextInputs) != 2 || m.TextOutput == "" {
		return fmt.Errorf("meta.json: 文本塔需要 2 个输入（input_ids/attention_mask）与 1 个输出")
	}
	if m.ImageModel == "" {
		m.ImageModel = "image_encoder.onnx"
	}
	if m.TextModel == "" {
		m.TextModel = "text_encoder.onnx"
	}
	// 预处理口径是历史踩坑点：早期文档与产物里写过 shortest_side/center，
	// 与官方 preprocessor_config.json（do_center_crop=false）不符。
	if m.Preprocess.Resize != "" && m.Preprocess.Resize != "stretch" {
		return fmt.Errorf("meta.json: 预处理 resize=%q 与实现不一致，只支持 stretch（见 design §7.2）", m.Preprocess.Resize)
	}
	return nil
}

// Mean 返回归一化均值，缺失时用 CLIP 官方值。
func (m *Meta) Mean() [3]float32 { return triple(m.Preprocess.Mean, defaultMean) }

// Std 返回归一化标准差，缺失时用 CLIP 官方值。
func (m *Meta) Std() [3]float32 { return triple(m.Preprocess.Std, defaultStd) }

func triple(v []float32, fallback [3]float32) [3]float32 {
	if len(v) != 3 {
		return fallback
	}
	return [3]float32{v[0], v[1], v[2]}
}
