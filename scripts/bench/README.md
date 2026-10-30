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

# 模型（CN-CLIP ViT-B/16 的 ONNX 视觉塔）
mkdir -p /tmp/bench/m && cp image_encoder.onnx /tmp/bench/m/vitb16.onnx
```

> `huggingface.co` 在目标机不可达，导出模型时用
> `HF_ENDPOINT=https://hf-mirror.com`。该镜像可能很慢，必要时在本地导出后传输。

## 关键陷阱

**GPU 测试必须经 `sg render` 执行**，否则 OpenVINO 报
`Device GPU is not available` 并**静默回退 CPU**，表现为 722 ms 而不是 111 ms，
极易误判为"iGPU 不可用"。

```bash
sg render -c "/tmp/bench/bin/python scripts/bench/bench_gpu_sweep.py"
```

## 脚本说明

| 脚本 | 用途 | 关键结论 |
|---|---|---|
| `bench_gpu_sweep.py` | GPU/CPU × batch size 扫描 | GPU FP16 bs=1 即饱和：111 ms/img，9.0 img/s；CPU 763 ms |
| `pipe2.py` | 端到端流水线（解码+推理） | 缩略图路径 8.6 img/s；多解码线程反而更慢 |
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
