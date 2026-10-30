// Package ai 封装 CN-CLIP 的图像/文本编码能力。
//
// 构建约束：只有 cgo 构建才包含 onnxruntime 绑定（backend_ort.go）。
// 非 cgo 构建（Windows 本地开发、没有 gcc 的环境）返回 ErrUnavailable，
// 上层据此把语义检索降级为 503，其余功能不受影响。
//
// 三条硬约束，改动前请先读 design 文档对应章节：
//   - 模型必须是静态 shape（§2.5）：动态 batch 会让 OpenVINO EP 在 GPU 上
//     静默输出错向量，不报错、不崩溃，只让检索整体失真；
//   - 预处理是「直接拉伸到 224×224」（§7.2），不是短边缩放 + 中心裁剪；
//   - 模型输出未归一化，L2 在 Go 侧统一做，避免两侧口径不一致。
package ai

import (
	"errors"
	"fmt"
	"image"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/zhangjh/mini-nas/server/internal/imgcodec"
)

// ErrUnavailable 表示当前构建或环境不支持推理。
//
// 它不是故障，是预期的降级路径：API 层据此返回 503，与去重关闭时的语义一致。
var ErrUnavailable = errors.New("语义检索不可用：当前构建未启用推理（需要 cgo + onnxruntime）")

// Config 是推理内核的启动参数。
type Config struct {
	// ModelDir 是模型产物目录（含 meta.json / vocab.txt / 两个 onnx）。
	ModelDir string
	// ModelID 是向量归属的模型标识，换模型时旧向量按此隔离。
	ModelID string
	// Device 支持 "GPU" / "CPU"。GPU 不可用时自动降级并在日志里告警。
	Device string
	// Precision 仅 GPU 生效，实测 FP16 余弦 0.99999。
	Precision string
	// ORTLib 是 libonnxruntime.so 的绝对路径；为空时用系统的默认搜索路径。
	ORTLib string
	// Threads 是单个会话的算子内并行度。CPU 侧实测加线程反而更慢，默认 1。
	Threads int
	// SelfTest 打开时启动会用 golden.npz 对拍，失败则拒绝启用。
	SelfTest bool
	// TextCacheSize 是文本查询向量的 LRU 容量。
	TextCacheSize int
}

// backend 是推理后端的抽象，唯一的实现差异是「有没有 cgo」。
type backend interface {
	// Device 返回实际生效的设备（GPU/CPU），可能与配置不同（降级）。
	Device() string
	// Name 返回后端标识，用于 /api/index/health。
	Name() string
	// Image 输入已预处理的 CHW 张量，输出未归一化的 512 维向量。
	Image(vec []float32) ([]float32, error)
	// Text 输入定长 input_ids 与 attention_mask，输出未归一化的向量。
	Text(ids, mask []int64) ([]float32, error)
	Close() error
}

// SelfTestReport 是启动自检结果，对外暴露在 /api/index/health。
type SelfTestReport struct {
	PreprocCos float64 `json:"preproc_cos"`
	ImageCos   float64 `json:"image_cos"`
	TextCos    float64 `json:"text_cos"`
	Threshold  float64 `json:"threshold"`
	Pass       bool    `json:"pass"`
}

// Status 是推理内核的运行时状态。
type Status struct {
	Available bool            `json:"available"`
	ModelID   string          `json:"model_id"`
	Device    string          `json:"device"`
	Backend   string          `json:"backend"`
	Dim       int             `json:"dim"`
	ImageSize int             `json:"image_size"`
	SeqLen    int             `json:"seq_len"`
	WarmupMS  int64           `json:"warmup_ms"`
	SelfTest  *SelfTestReport `json:"selftest,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// Engine 是推理入口。并发安全：内部把推理串行化。
//
// 串行的原因不是懒得加锁：GPU 只有一个，batch=1 已经饱和（8.3 vs 9.7 img/s），
// 并发提交只会让每个请求都变慢。
type Engine struct {
	cfg  Config
	meta Meta
	tok  *Tokenizer
	be   backend

	inferMu sync.Mutex
	textMu  sync.Mutex
	cache   *lruCache

	statusMu sync.RWMutex
	status   Status
}

// New 加载模型并完成预热与自检。
//
// 返回 ErrUnavailable 表示环境不支持（非 cgo 构建、缺库、缺模型），
// 调用方应把 AI 关掉而不是让服务启动失败。
func New(cfg Config) (*Engine, error) {
	if cfg.ModelDir == "" {
		return nil, fmt.Errorf("未配置模型目录")
	}
	if cfg.Threads <= 0 {
		cfg.Threads = 1
	}
	if cfg.TextCacheSize <= 0 {
		cfg.TextCacheSize = 256
	}

	meta, err := LoadMeta(cfg.ModelDir)
	if err != nil {
		return nil, err
	}
	if cfg.ModelID == "" {
		cfg.ModelID = meta.ModelID
	}

	tok, err := LoadTokenizer(filepath.Join(cfg.ModelDir, "vocab.txt"), meta.SeqLen)
	if err != nil {
		return nil, err
	}

	be, err := newBackend(cfg, meta)
	if err != nil {
		return nil, err
	}

	e := &Engine{
		cfg:   cfg,
		meta:  meta,
		tok:   tok,
		be:    be,
		cache: newLRU(cfg.TextCacheSize),
	}
	e.status = Status{
		Available: true,
		ModelID:   cfg.ModelID,
		Device:    be.Device(),
		Backend:   be.Name(),
		Dim:       meta.EmbedDim,
		ImageSize: meta.ImageSize,
		SeqLen:    meta.SeqLen,
	}

	start := time.Now()
	if err := e.warmup(); err != nil {
		be.Close()
		return nil, fmt.Errorf("预热失败: %w", err)
	}
	e.status.WarmupMS = time.Since(start).Milliseconds()

	if cfg.SelfTest {
		report, err := e.SelfTest(cfg.ModelDir, 0.999)
		if err != nil {
			be.Close()
			e.status.Available = false
			e.status.Error = err.Error()
			return nil, fmt.Errorf("自检失败（GPU 路径异常时不会报错、只会给出错向量，必须修好再上线）: %w", err)
		}
		e.status.SelfTest = &report
		if !report.Pass {
			be.Close()
			e.status.Available = false
			e.status.Error = "自检余弦低于阈值"
			return nil, fmt.Errorf("自检未通过：preproc=%.6f image=%.6f text=%.6f（阈值 %.3f）",
				report.PreprocCos, report.ImageCos, report.TextCos, report.Threshold)
		}
	}

	log.Printf("semantic engine ready: model=%s device=%s backend=%s warmup=%dms",
		e.status.ModelID, e.status.Device, e.status.Backend, e.status.WarmupMS)
	return e, nil
}

// warmup 跑一次空推理，把 GPU 首次编译（3.7~5.2s）的成本挪到启动阶段。
func (e *Engine) warmup() error {
	size := e.meta.ImageSize
	blank := image.NewNRGBA(image.Rect(0, 0, size, size))
	for i := 0; i < len(blank.Pix); i += 4 {
		blank.Pix[i] = 127
		blank.Pix[i+1] = 127
		blank.Pix[i+2] = 127
		blank.Pix[i+3] = 255
	}
	_, err := e.EncodeImage(blank)
	return err
}

// EncodeImage 把图片编码为 L2 归一化的向量。
func (e *Engine) EncodeImage(img image.Image) ([]float32, error) {
	if img == nil {
		return nil, fmt.Errorf("空图片")
	}
	vec := Preprocess(img, e.meta.ImageSize, e.meta.Mean(), e.meta.Std())
	return e.EncodeTensor(vec)
}

// EncodeTensor 对已预处理的张量做推理，返回 L2 归一化的向量。
//
// 拆出这一层是为了让索引流水线能把「解码 + 预处理」（CPU 密集）与
// 「推理」（GPU 串行）放在不同 goroutine 里，同时只解码一次。
func (e *Engine) EncodeTensor(vec []float32) ([]float32, error) {
	e.inferMu.Lock()
	out, err := e.be.Image(vec)
	e.inferMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("图像推理失败: %w", err)
	}
	if len(out) != e.meta.EmbedDim {
		return nil, fmt.Errorf("输出维度异常：期望 %d，实际 %d", e.meta.EmbedDim, len(out))
	}
	return L2Normalize(out), nil
}

// Preprocess 用模型声明的尺寸与归一化参数预处理图片。
func (e *Engine) Preprocess(img image.Image) []float32 {
	return Preprocess(img, e.meta.ImageSize, e.meta.Mean(), e.meta.Std())
}

// ImageSize 返回模型输入边长。
func (e *Engine) ImageSize() int { return e.meta.ImageSize }

// EncodeImageFile 解码文件后编码。hint 传给解码器做降采样提示。
func (e *Engine) EncodeImageFile(absPath string, hint int) ([]float32, error) {
	if hint <= 0 {
		hint = e.meta.ImageSize
	}
	img, err := imgcodec.DecodeFile(absPath, hint)
	if err != nil {
		return nil, err
	}
	return e.EncodeImage(img)
}

// EncodeText 把查询文本编码为 L2 归一化的向量，带 LRU。
func (e *Engine) EncodeText(text string) ([]float32, error) {
	if text == "" {
		return nil, fmt.Errorf("空查询")
	}
	if v, ok := e.cache.Get(text); ok {
		return v, nil
	}

	// 文本塔与图像塔共用同一块 GPU，必须与图像推理互斥；
	// 但 tokenize 可以并行，放在锁外。
	ids, mask := e.tok.Encode(text)
	e.inferMu.Lock()
	out, err := e.be.Text(ids, mask)
	e.inferMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("文本推理失败: %w", err)
	}
	if len(out) != e.meta.EmbedDim {
		return nil, fmt.Errorf("输出维度异常：期望 %d，实际 %d", e.meta.EmbedDim, len(out))
	}

	vec := L2Normalize(out)
	e.textMu.Lock()
	e.cache.Put(text, vec)
	e.textMu.Unlock()
	return vec, nil
}

// Tokenize 暴露分词结果，便于排查「查不到结果」是分词还是向量的问题。
func (e *Engine) Tokenize(text string) []string { return e.tok.Tokenize(text) }

// Dim 返回向量维度。
func (e *Engine) Dim() int { return e.meta.EmbedDim }

// ModelID 返回向量归属的模型标识。
func (e *Engine) ModelID() string { return e.cfg.ModelID }

// Status 返回运行时状态快照。
func (e *Engine) Status() Status {
	e.statusMu.RLock()
	defer e.statusMu.RUnlock()
	return e.status
}

// Close 释放会话与张量。
func (e *Engine) Close() error {
	if e.be == nil {
		return nil
	}
	return e.be.Close()
}
