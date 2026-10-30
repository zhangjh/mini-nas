//go:build cgo

package ai

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// gpuRenderNode 是 iGPU 的渲染节点。
//
// OpenVINO EP 在 GPU 不可用时会**静默回退 CPU**（不报错，只是慢 6 倍），
// 因此这里先看设备节点是否存在，避免把 CPU 执行误报成 GPU。
const gpuRenderNode = "/dev/dri/renderD128"

// onnxruntime 的环境初始化是进程级的，只能做一次。
var (
	ortOnce sync.Once
	ortErr  error
)

// ortBackend 基于 onnxruntime C API（通过 yalue/onnxruntime_go 动态加载）。
//
// 选它而不是自己写 CGO 绑定的理由：
//   - OrtApi 是一张函数指针表，手写绑定要精确对齐字段顺序，跨版本极易错位；
//   - 该库用 dlopen 动态加载，构建期不需要 onnxruntime 头文件，镜像里只放 .so；
//   - 一套运行时同时覆盖 GPU（OpenVINO EP）与 CPU（ORT 自带 MLAS 内核）。
type ortBackend struct {
	meta   Meta
	device string
	name   string
	img    *imgRunner
	txt    *txtRunner
}

type imgRunner struct {
	sess *ort.AdvancedSession
	in   *ort.Tensor[float32]
	out  *ort.Tensor[float32]
}

type txtRunner struct {
	sess *ort.AdvancedSession
	ids  *ort.Tensor[int64]
	mask *ort.Tensor[int64]
	out  *ort.Tensor[float32]
}

func newBackend(cfg Config, meta Meta) (backend, error) {
	initORT(cfg.ORTLib)
	if ortErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, ortErr)
	}

	imgPath := filepath.Join(cfg.ModelDir, meta.ImageModel)
	txtPath := filepath.Join(cfg.ModelDir, meta.TextModel)
	for _, p := range []string{imgPath, txtPath} {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("%w: 模型文件缺失 %s（先跑 scripts/setup_model.sh）", ErrUnavailable, p)
		}
	}

	wantGPU := strings.EqualFold(cfg.Device, "GPU") || cfg.Device == ""
	precision := cfg.Precision
	if precision == "" {
		precision = "FP16"
	}

	b := &ortBackend{meta: meta, device: "CPU", name: "onnxruntime-cpu"}

	if wantGPU && !gpuAvailable() {
		log.Printf("WARN 未检测到 %s，语义索引降级到 CPU（约慢 6 倍）", gpuRenderNode)
		wantGPU = false
	}

	// 先按目标设备建会话；GPU 建不起来再退回 CPU。
	// 注意不能「先建 GPU 再改 options」——EP 一旦 append 就摘不掉，必须重建。
	img, txt, err := openSessions(imgPath, txtPath, meta, wantGPU, precision, cfg.Threads)
	if err != nil && wantGPU {
		log.Printf("WARN GPU 会话创建失败，降级到 CPU: %v", err)
		wantGPU = false
		img, txt, err = openSessions(imgPath, txtPath, meta, false, precision, cfg.Threads)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: 创建推理会话失败: %v", ErrUnavailable, err)
	}

	b.img, b.txt = img, txt
	if wantGPU {
		b.device = "GPU"
		b.name = "onnxruntime-openvino"
	}
	return b, nil
}

func initORT(libPath string) {
	ortOnce.Do(func() {
		if libPath != "" {
			if _, err := os.Stat(libPath); err != nil {
				ortErr = fmt.Errorf("onnxruntime 库不存在: %s", libPath)
				return
			}
			ort.SetSharedLibraryPath(libPath)
		}
		ortErr = ort.InitializeEnvironment()
	})
}

func gpuAvailable() bool {
	_, err := os.Stat(gpuRenderNode)
	return err == nil
}

func openSessions(imgPath, txtPath string, meta Meta, gpu bool, precision string, threads int) (*imgRunner, *txtRunner, error) {
	img, err := openImageSession(imgPath, meta, gpu, precision, threads)
	if err != nil {
		return nil, nil, err
	}
	txt, err := openTextSession(txtPath, meta, gpu, precision, threads)
	if err != nil {
		img.destroy()
		return nil, nil, err
	}
	return img, txt, nil
}

func newSessionOptions(gpu bool, precision string, threads int) (*ort.SessionOptions, error) {
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	// CPU 侧实测加线程反而更慢（解码与推理抢同一颗 die 的功耗/带宽），默认 1。
	if err := opts.SetIntraOpNumThreads(threads); err != nil {
		opts.Destroy()
		return nil, err
	}
	if !gpu {
		// 不 append 任何 EP：ORT 自带 MLAS 内核比 OpenVINO CPU 插件快 2.6 倍
		// （N5105 无 AVX2/FMA）。
		return opts, nil
	}
	err = opts.AppendExecutionProviderOpenVINO(map[string]string{
		"device_type": "GPU",
		"precision":   precision,
	})
	if err != nil {
		opts.Destroy()
		return nil, fmt.Errorf("挂载 OpenVINO EP 失败: %w", err)
	}
	return opts, nil
}

func openImageSession(path string, meta Meta, gpu bool, precision string, threads int) (*imgRunner, error) {
	size := int64(meta.ImageSize)
	in, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 3, size, size))
	if err != nil {
		return nil, fmt.Errorf("创建视觉输入张量: %w", err)
	}
	out, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(meta.EmbedDim)))
	if err != nil {
		in.Destroy()
		return nil, fmt.Errorf("创建视觉输出张量: %w", err)
	}

	opts, err := newSessionOptions(gpu, precision, threads)
	if err != nil {
		in.Destroy()
		out.Destroy()
		return nil, err
	}
	defer opts.Destroy()

	sess, err := ort.NewAdvancedSession(path,
		[]string{meta.ImageInput}, []string{meta.ImageOutput},
		[]ort.Value{in}, []ort.Value{out}, opts)
	if err != nil {
		in.Destroy()
		out.Destroy()
		return nil, fmt.Errorf("加载视觉塔 %s: %w", path, err)
	}
	return &imgRunner{sess: sess, in: in, out: out}, nil
}

func openTextSession(path string, meta Meta, gpu bool, precision string, threads int) (*txtRunner, error) {
	seq := int64(meta.SeqLen)
	ids, err := ort.NewEmptyTensor[int64](ort.NewShape(1, seq))
	if err != nil {
		return nil, fmt.Errorf("创建文本输入张量: %w", err)
	}
	mask, err := ort.NewEmptyTensor[int64](ort.NewShape(1, seq))
	if err != nil {
		ids.Destroy()
		return nil, fmt.Errorf("创建 attention_mask 张量: %w", err)
	}
	out, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(meta.EmbedDim)))
	if err != nil {
		ids.Destroy()
		mask.Destroy()
		return nil, fmt.Errorf("创建文本输出张量: %w", err)
	}

	opts, err := newSessionOptions(gpu, precision, threads)
	if err != nil {
		ids.Destroy()
		mask.Destroy()
		out.Destroy()
		return nil, err
	}
	defer opts.Destroy()

	sess, err := ort.NewAdvancedSession(path,
		meta.TextInputs, []string{meta.TextOutput},
		[]ort.Value{ids, mask}, []ort.Value{out}, opts)
	if err != nil {
		ids.Destroy()
		mask.Destroy()
		out.Destroy()
		return nil, fmt.Errorf("加载文本塔 %s: %w", path, err)
	}
	return &txtRunner{sess: sess, ids: ids, mask: mask, out: out}, nil
}

func (b *ortBackend) Device() string { return b.device }
func (b *ortBackend) Name() string   { return b.name }

func (b *ortBackend) Image(vec []float32) ([]float32, error) {
	if b.img == nil {
		return nil, fmt.Errorf("视觉塔未加载")
	}
	return b.img.run(vec)
}

func (b *ortBackend) Text(ids, mask []int64) ([]float32, error) {
	if b.txt == nil {
		return nil, fmt.Errorf("文本塔未加载")
	}
	return b.txt.run(ids, mask)
}

func (b *ortBackend) Close() error {
	if b.img != nil {
		b.img.destroy()
		b.img = nil
	}
	if b.txt != nil {
		b.txt.destroy()
		b.txt = nil
	}
	return nil
}

func (r *imgRunner) run(vec []float32) ([]float32, error) {
	data := r.in.GetData()
	if len(vec) != len(data) {
		return nil, fmt.Errorf("输入张量尺寸不匹配：期望 %d，实际 %d", len(data), len(vec))
	}
	// GetData 返回的是张量底层内存的切片，直接 copy 即写入 ORT 的输入。
	copy(data, vec)
	if err := r.sess.Run(); err != nil {
		return nil, err
	}
	out := make([]float32, len(r.out.GetData()))
	copy(out, r.out.GetData())
	return out, nil
}

func (r *imgRunner) destroy() {
	if r.sess != nil {
		r.sess.Destroy()
		r.sess = nil
	}
	if r.in != nil {
		r.in.Destroy()
		r.in = nil
	}
	if r.out != nil {
		r.out.Destroy()
		r.out = nil
	}
}

func (r *txtRunner) run(ids, mask []int64) ([]float32, error) {
	if len(ids) != len(r.ids.GetData()) || len(mask) != len(r.mask.GetData()) {
		return nil, fmt.Errorf("文本输入长度不匹配：期望 %d", len(r.ids.GetData()))
	}
	copy(r.ids.GetData(), ids)
	copy(r.mask.GetData(), mask)
	if err := r.sess.Run(); err != nil {
		return nil, err
	}
	out := make([]float32, len(r.out.GetData()))
	copy(out, r.out.GetData())
	return out, nil
}

func (r *txtRunner) destroy() {
	if r.sess != nil {
		r.sess.Destroy()
		r.sess = nil
	}
	if r.ids != nil {
		r.ids.Destroy()
		r.ids = nil
	}
	if r.mask != nil {
		r.mask.Destroy()
		r.mask = nil
	}
	if r.out != nil {
		r.out.Destroy()
		r.out = nil
	}
}
