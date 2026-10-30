# 语义索引预研基准脚本

这些脚本用于在**目标机**上复现 `docs/design-semantic-index.md` 中的性能数据。
不入生产构建，仅作预研留档。

## 环境准备（一次性）

```bash
# iGPU 运行时（Ubuntu 24.04）
sudo apt-get install -y intel-opencl-icd libze-intel-gpu1
sudo usermod -aG render $USER     # 需重新登录生效

# Python 环境
python3 -m venv /tmp/bench
/tmp/bench/bin/pip install onnxruntime-openvino numpy pillow pillow-heif
# 只做原生 OV 对照时才需要：
/tmp/bench/bin/pip install openvino

# 模型权重（HF 不可达，走 ModelScope）
ID=AI-ModelScope/chinese-clip-vit-base-patch16
mkdir -p /tmp/bench/cnclip-b16 && cd /tmp/bench/cnclip-b16
for f in config.json preprocessor_config.json vocab.txt pytorch_model.bin; do
  curl -L "https://www.modelscope.cn/api/v1/models/$ID/repo?Revision=master&FilePath=$f" -o "$f"
done
# 导出（约 4 分钟，含四段校验）
sg render -c "/tmp/bench/bin/python scripts/export_clip_onnx.py \
  --model /tmp/bench/cnclip-b16 --out /tmp/bench/out/cn-clip-vit-b16"
```

## 两个必须知道的陷阱

### 1. GPU 测试必须经 `sg render` 执行

否则 OpenVINO 报 `Device GPU is not available` 并**静默回退 CPU**，
表现为 722 ms 而不是 121 ms，极易误判为"iGPU 不可用"。

```bash
sg render -c "/tmp/bench/bin/python scripts/bench/bench_gpu_sweep.py"
```

### 2. 只测速度不测数值的基准等于没测

**这是本预研踩过的最大的坑。** 早期用动态 batch 的 ONNX 测 GPU，得到
「111 ms/img，快 6.9 倍」的漂亮数字，但那个模型的 GPU 输出是**512 维全零向量**——
快是因为它根本没算出东西。CPU 路径完全正常，所以没人起疑。

正确做法：所有性能脚本都必须同时输出与 PyTorch 参考的余弦（`cos_vs_torch` 一列），
低于 0.999 直接判为无效测量。`scripts/export_clip_onnx.py` 已把这条做进导出流程
（含 GPU 对拍），`bench_exported.py` / `diag_*.py` 也都带这一列。

## 脚本说明

| 脚本 | 用途 | 关键结论 |
|---|---|---|
| `export_clip_onnx.py`（上级目录） | 导出静态 shape ONNX + 四段校验 | 视觉 1.00000000 / 文本 0.99999988 / GPU FP16 0.99999 |
| `make_preproc_golden.py`（上级目录） | 生成预处理黄金样本 | 641×427 合成图 + 官方 processor 期望张量，手算复核 diff=0 |
| `ab_preproc.py` | **判定正确预处理：拉伸 vs 中心裁剪** | 官方拉伸 margin +0.0725 > 中心裁剪 +0.0669，两者向量余弦 0.974 |
| `bench_exported.py` | 导出产物的 CPU/GPU 多配置性能 + 保真度 | GPU FP16 121 ms，CPU 747 ms，均 cos > 0.99999 |
| `diag_ovgpu.py` | **复现动态 shape 的 GPU 全零输出** | 静态/动态对照，证明 bug 来源 |
| `diag_ort_ov_static.py` | ORT-OV EP 静态 vs 动态 shape | 静态下 GPU 121 ms / cos 0.99999511，正常 |
| `diag_ov_native.py` | 原生 OpenVINO 与 ORT-OV 对照 | 原生 OV GPU 正常（130 ms），证明坏的是动态 shape 不是 GPU |
| `bench_ov_native.py` | 原生 OV 各 batch 扫描 | GPU f16 bs=16 98 ms；**CPU 仅 0.53 img/s** |
| `bench_ov_cpu_tune.py` | OV CPU 线程/hint 调优 | 单线程 2778 → 4 线程 1891 ms，并行效率仅 1.47×，不可用 |
| `fetch_models.sh` | 分块拉取模型产物（CF 隧道会断） | 16MB 一块，逐块重试 + md5 校验 |
| `bench_gpu_sweep.py` | GPU/CPU × batch size 扫描 | ⚠️ 用的是动态 shape 模型，数字仅作历史参考，结论已被推翻 |
| `pipe2.py` | 端到端流水线（解码+推理） | ⚠️ 同上，推理段基于错误模型 |
| `io.py` | IO 与解码成本拆解 | 注意：该脚本按"每轮 N 张"计时，需再除以 N |
| `thumb.py` | 真实缩略图端到端 | 320 缩略图 → 224 仅 3~5 ms |
| `dec.py` | 全分辨率解码 vs DCT 降采样 | libjpeg `draft` 139.7 → 18.6 ms（7.5×） |
| `heic.py` | HEIC 解码分析 | 12 MP HEIC 676 ms，多线程无效，无内嵌缩略图 |
| `sz.py` | 缩略图短边尺寸分布 | 96.7% 短边 ≥ 224，可直接喂 CLIP |
| `gen_imgs.py` | 生成测试图（JPEG + HEIC） | — |
| `thumb_coverage.py` | 精确统计缩略图覆盖率与缓存键碰撞 | 13,980 张，命中 4,508（32.2%），缺失 9,472，孤儿 11，0 碰撞 |
| `thumb_gen.py` | ffmpeg 批量生成缩略图吞吐 | 单进程 321 ms；4 进程摊薄 85.7 ms |
| `thumb_gen_inproc.py` | **进程内生成缩略图**（libjpeg 路径） | 全解码 180 ms；DCT 降采样 45.1 ms，4 线程摊薄 **12.8 ms** |
| `expL.py` | 导出 ViT-L/14 视觉塔 | **未跑通**：模型下载受阻，见设计文档风险 R5 |

> **缩略图结论**：补齐 9,472 张，进程内 4 线程约 **2 分钟**；ffmpeg 4 进程需 13.5 分钟；
> 现有纯 Go `imaging` 需 83 分钟。所以用 libjpeg-turbo 进程内生成，不用 ffmpeg。

## Go 侧解码基准

纯 Go 解码（`image/jpeg` + `imaging`）与 libjpeg-turbo 的差距是选型的核心依据：

| 路径 | 耗时 |
|---|---|
| 12 MP JPG → 224，Go 全解码 + bilinear | 409 ms |
| 12 MP JPG → 224，Go 全解码 + Lanczos | 530 ms |
| 12 MP JPG → 224，libjpeg-turbo `scale_denom` | 17 ms |

测量方法：交叉编译一个遍历 `/mnt/nas/照片` 的小程序，`GOOS=linux go build` 后
用 `ssh 'cat > /tmp/benchimg' < ./benchimg` 上传执行（该环境 `scp` 不可用）。
