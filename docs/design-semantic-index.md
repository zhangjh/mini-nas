# MiniNas 语义检索 + 重复文件检测 设计与实施方案

> 状态：预研完成，待开发
> 目标机器：Intel Celeron N5105 / Jasper Lake / 7.8 GB / Ubuntu 24.04
> 部署形态：docker-compose（server + web）

---

## 0. 预研结论摘要

先给结论，后面是依据。

| 问题 | 结论 |
|---|---|
| 端侧 Agent | **不做**。索引与检索全部在服务端，客户端零推理 |
| CLIP 模型 | **CN-CLIP ViT-B/16（512 维）**，中文原生，不走"单语种模型 + 翻译" |
| 推理加速 | **iGPU 可用**，ORT + OpenVINO EP，GPU FP16 比 CPU 快 **6.0 倍**（722ms → 121ms） |
| 推理后端 | **ONNX Runtime C API + OpenVINO EP**（一套运行时同时覆盖 CPU/GPU） |
| **模型 shape** | **必须静态**。动态 batch 会让 OpenVINO EP 在 GPU 上静默输出错向量（见 §2.5） |
| **预处理** | **直接拉伸到 224×224**，不是「短边缩放 + 中心裁剪」。与 OpenAI CLIP 惯例相反，实测官方拉伸更优（见 §7.2） |
| 索引输入 | **缩略图优先**，缺失时解原图并回写缩略图 |
| 缩略图 | 现有实现有 4 个缺陷（无失效/无 mtime 校验/键碰撞/解码慢），**已改造完成**；批量补齐 9,472 张约 2 分钟 |
| 图像解码 | **libjpeg-turbo（DCT 降采样）**。纯 Go 全解码慢 24 倍，不可接受 |
| 向量检索 | SQLite 存 BLOB + **内存暴力余弦**。14k 条仅 28 MB，无需 HNSW/FAISS |
| 全量索引耗时 | **14k 张约 29 分钟**（GPU 推理 121ms 是硬下限） |
| 量化 | **不做**。不牺牲检索精度换空间 |

一句话：**GPU 推理 121ms 是天花板，其余环节都必须压到可忽略，否则总耗时会被放大。**

> **与初版预研的差异（重要）**：初版写的「GPU 111ms、快 6.9 倍」是无效数据。
> 那次基准只测了速度、没校验输出，而当时的动态 shape 模型在 GPU 上返回的是
> 全零向量。修正后结论仍然成立（iGPU 确实能用），但**后端从"原生 OpenVINO
> C API"改为"ORT C API + OpenVINO EP"**，且模型必须导出静态 shape。详见 §2.5。

### 从哪开始（给接手的实现者）

已完成：P0 去重、P0.1 挂载点修复、P1 模型导出、P2.5 缩略图改造。
**下一步是 P2：`internal/ai` 推理内核**，它是后面 P3/P4 的前置。

P2 的四个硬约束，写代码前先记住：

1. **模型静态 shape**，`[1,3,224,224]` 和 `[1,64]`，不要"顺手支持动态 batch"（§2.5）
2. **预处理是拉伸**，不是中心裁剪（§7.2）
3. **文本池化用 CLS token**，不是 `pooler_output`（CN-CLIP 的 pooler 是 None）
4. **`OrtApi` 是函数指针表**，CGO 里必须用 C 静态内联函数转发，不能直接跨语言取成员

验收方式：跑 `preproc_golden.npz`（预处理级）和 `golden.npz`（模型级）两个夹具，
两者都 > 0.999 才算通过。夹具已入库，**不需要 Python 环境**。

模型权重不在仓库里，首次开发先跑一次 `scripts/setup_model.sh`；
目标机上已经有一份，可以直接从 `/mnt/nas/dev/mini-nas/server/models/` 复制。

---

## 1. 需求与范围

### 1.1 功能需求

| 编号 | 需求 | 优先级 |
|---|---|---|
| F1 | 基于 MD5 的重复文件检测（上传时跳过 + 全量扫描） | P0（已完成） |
| F2 | 重复文件合并（硬链接 / 移入回收站） | P0（已完成） |
| F3 | 图片语义检索（中文自然语言） | P1 |
| F4 | 以图搜图（相似图片） | P1 |
| F5 | 后台索引任务（可观测、可取消、可续跑） | P1 |
| F6 | HEIC 图片参与索引 | P1 |
| F7 | Web 端检索界面 | P2 |
| F8 | Android 端检索入口 | P2 |

### 1.2 明确不做

- 端侧 Agent、端侧推理
- 视频内容理解（1,883 个 MP4/MOV 只跳过，不索引）
- 人脸识别 / OCR / 物体框选
- 多用户隔离的向量空间（沿用现有单租户模型）

### 1.3 现有库实况（用于容量规划）

统计自 `/mnt/nas/照片`：

| 项 | 数量 |
|---|---|
| JPG | 13,700 |
| PNG | 253 |
| JPEG | 25 |
| **图片合计** | **13,978** |
| MP4 / MOV（跳过） | 1,883 |
| **HEIC** | **0** |
| 已有缩略图缓存 | 4,519 个文件，其中 **4,508 命中（32.2%）**、11 个孤儿；**缺失 9,472（67.8%）** |

缩略图规格：`imaging.Fit(320, 320)` + JPEG q80，平均 16.7 KB。
短边尺寸分布：**96.7%（4,371/4,519）短边 ≥ 224**，仅 121 张（2.7%）超宽图短边 < 224。

---

## 2. 实测基线

所有数据在目标机实测，非估算。复现方法见附录 A。

### 2.1 推理（ViT-B/16，224×224，静态 shape，batch=1）

| 后端 | 设备/精度 | ms/img | img/s |
|---|---|---|---|
| ORT CPU EP | CPU / FP32 | 722 | 1.39 |
| ORT + OpenVINO EP | CPU / FP32 | 747 | 1.34 |
| 原生 OpenVINO API | CPU / FP32 | 1431–1891 | 0.53–0.70 |
| **ORT + OpenVINO EP** | **GPU / FP16** | **121** | **8.3** |
| ORT + OpenVINO EP | GPU / FP16，batch=16 | 103 | 9.7 |
| 原生 OpenVINO API | GPU / FP16 | 130 | 7.7 |
| 原生 OpenVINO API | GPU / FP16，batch=16 | 98 | 10.2 |
| ORT + OpenVINO EP | GPU / FP32 | 258 | 3.9 |

文本编码器（1×64，GPU FP16 / CPU FP32）：

| 后端 | ms |
|---|---|
| 原生 OpenVINO GPU FP16 | 19.4 |
| ORT CPU EP | 52.4 |
| 原生 OpenVINO CPU FP32 | 62.3 |

关键观察：
- **batch=1 就基本饱和**（8.3 vs 9.7 img/s），不做批处理，省掉补齐逻辑
- CPU 侧多进程无收益，加线程反而更慢（解码与推理抢同一颗 die 的功耗/带宽）
- **OpenVINO 的 CPU 插件在这颗 Tremont 核上很差**：单线程 2778ms → 4 线程 1891ms，
  并行效率仅 1.47×，比 ORT 自带的 MLAS 内核慢 2.6 倍（N5105 无 AVX2/FMA）。
  所以 CPU 回退路径靠 ORT CPU EP，不靠 OpenVINO CPU
- GPU 首次加载（含编译）3.7–5.2 s，需要预热

### 2.2 图像解码 → 224

| 路径 | 耗时 |
|---|---|
| 320×240 缩略图 → 224（Python/libjpeg） | 3.0 ms |
| 320×240 缩略图 → 224（Go + bilinear） | 4.7 ms |
| 12 MP JPG → 224，libjpeg `scale_denom` | **17.2 ms** |
| 12 MP JPG → 224，Go 全解码 + bilinear | **409 ms** |
| 12 MP JPG → 224，Go 全解码 + Lanczos | **530 ms** |
| 12 MP HEIC → 224（libheif） | 676 ms |

**纯 Go 全解码慢 24 倍**，这是必须用 libjpeg-turbo 的直接原因。

### 2.3 端到端（解码 + 推理）

| 索引来源 | 解码 | 推理 | 合计 | 吞吐 | 14k 张 |
|---|---|---|---|---|---|
| 320 缩略图 | 4.7 | 121 | **126 ms** | 7.9 img/s | **29 min** |
| 12 MP 原图（libjpeg draft） | 17 | 121 | 138 ms | 7.2 img/s | 32 min |
| 12 MP 原图（Go 全解码） | 409 | 121 | 530 ms | 1.9 img/s | 124 min |
| HEIC 12 MP | 676 | 121 | 797 ms | 1.3 img/s | 3 h |

> **基准口径说明**：上述端到端测试的预处理用的是「直接 resize 到 224×224」（拉伸），
> 与最终实现一致。注意这与 OpenAI CLIP 的惯例（短边缩放 + 中心裁剪）相反，
> 依据是随权重发布的 `preprocessor_config.json` 里 `do_center_crop: false`，
> 以及 §7.2 的 A/B 实测（拉伸的图文区分度更高）。**不要凭直觉改成裁剪。**

### 2.4 加速路径探索结论

- ✅ **iGPU 可用**：需 `intel-opencl-icd` + `libze-intel-gpu1`，用户在 `render` 组
  （进程必须能访问 `/dev/dri/renderD128`，容器要透传设备）
- ✅ **ORT + OpenVINO EP 是可行组合**，但**必须静态 shape**，见 §2.5
- ❌ **原生 OpenVINO CPU 插件不可用**：比 ORT CPU EP 慢 2.6 倍，Tremont 无 AVX2/FMA
- ❌ **多线程无收益**：CPU 已饱和，加线程反而更慢
- ❌ **量化不考虑**：用户明确要求保精度
- ❌ **降级到 MobileCLIP2 + 翻译**：不需要了，GPU 已够快

### 2.5 动态 shape 在 OpenVINO EP 上的静默错误（关键发现）

同一份 ONNX、同一输入，只改 shape 声明，结果天差地别：

| shape | 设备 | 输出 | 与 PyTorch 余弦 | 结论 |
|---|---|---|---|---|
| 动态 batch | CPU EP | 正常 | 1.00000000 | ✅ |
| 动态 batch | GPU FP16 | **512 维全 0** | 0.0 | ❌ 静默错误 |
| 动态 batch | GPU FP32 | 全 0 或数值全错 | 0.06 ~ 0.51 | ❌ 静默错误 |
| 静态 1×3×224×224 | GPU FP16 | 正常 | **0.99999511** | ✅ |
| 静态 1×3×224×224 | CPU FP32 | 正常 | 1.00000000 | ✅ |

危害在于**不报错、不崩溃、不降级**：调用方拿到的是一组合法的 float32，
只有和参考实现对比才发现全错。如果直接上线，检索结果会整体失真而无人察觉。
而且同一配置多次运行结果还会变（一次全零、一次余弦 0.51），像未初始化内存，
不是稳定的数值误差 —— 所以不可能是 FP16 精度问题。

四重防线（已落在 `scripts/export_clip_onnx.py` 里，导出即校验）：
1. 模型一律导出静态 shape（图像 1×3×224×224，文本 1×64）
2. 校验里包含「ONNX vs PyTorch 余弦 > 0.999」
3. 校验里包含「GPU FP16 vs PyTorch 余弦 > 0.999」，GPU 不可用时显式 SKIP 而非 PASS
4. 校验里包含「同输入连跑两次结果完全一致」，排除未初始化内存导致的抖动

> 注：这个 bug 也解释了为什么初版预研的 111ms 不可信 —— 那次测量用的正是
> 动态 shape 模型，速度快是因为它根本没算出东西。

---

## 3. 关键决策（ADR）

### D1 模型：CN-CLIP ViT-B/16，512 维

**决策**：使用 `OFA-Sys/chinese-clip-vit-base-patch16`。

**理由**：
- 中文原生，文本塔是 RoBERTa-wwm，不需要"英文模型 + 翻译"的二次误差
- 17.6 GFLOPs，iGPU 上 8.3 img/s，14k 张 29 分钟
- 512 维向量，14k 条只占 28 MB

**否决项**：
- ViT-L/14（768 维，80 GFLOPs）：质量更好，但 4.5× FLOPs 推算约 500 ms/img，14k 需 ~2 小时，超出"一万张不能几个小时"的约束。**未实测**（模型下载受阻，见风险 R5）
- MobileCLIP2-S0：1.5 GFLOPs 很快，但文本塔是英文，必须翻译，引入误差且违背"中文原生"要求

**模型文件不入库**，用 `scripts/setup_model.sh` 复现（理由见 §11.3），也不入 Docker 镜像，
运行时只读挂载。

### D2 推理后端：ONNX Runtime C API + OpenVINO EP

**决策**：Go 通过 CGO 调用 **onnxruntime C API**（`OrtGetApiBase`），provider 选
`OpenVINOExecutionProvider`，`device_type` 在 `GPU` / `CPU` 间切换。模型导出静态 shape。

**理由**：
- **一套运行时同时覆盖 CPU 和 GPU**：GPU FP16 121 ms，CPU FP32 747 ms，只是
  `provider_options` 里换个字符串
- 官方 `onnxruntime-openvino` 预编译包已自带完整的 OpenVINO 运行库，
  **不需要 `--use_openvino` 自编译**（这是初版判断错的地方）：
  ```
  onnxruntime/capi/
    libonnxruntime.so.1.24.1
    libonnxruntime_providers_openvino.so
    libopenvino.so / libopenvino_c.so / libopenvino_intel_gpu_plugin.so
    libopenvino_intel_cpu_plugin.so / libopenvino_onnx_frontend.so / libtbb.so
  ```
  容器里直接把这批 `.so` 拷进去、设 `LD_LIBRARY_PATH` 即可
- ORT 的 C API 是稳定 ABI（`OrtApi` 函数表），CGO 绑定面比 OpenVINO C API 更小
- CPU 侧 ORT 自带 MLAS 内核比 OpenVINO CPU 插件快 2.6 倍（722 vs 1891 ms），
  同一套运行时天然拿到这个优势

**否决项**：
- 原生 OpenVINO C API：GPU 性能相当（130 ms），但 **CPU 只有 0.53 img/s**，
  等于放弃 CPU 回退路径；且要多维护一套绑定
- Python 子进程 sidecar：引入 Python 运行时，破坏单二进制交付
- 动态 shape：GPU 上静默输出错向量，见 §2.5

**降级策略**：`device` 按 `GPU → CPU` 探测。GPU 不可用时降级并**在日志与
`/semantic/status` 里显式暴露 `device=cpu`**，因为此时索引会慢 6 倍（14k 需约 3 小时），
用户必须知道。启动时若 GPU 探测失败，额外打一条 WARN。

### D3 图像解码：libjpeg-turbo（CGO）+ libheif（CGO）

**决策**：
- JPEG 用 libjpeg-turbo，启用 `scale_denom`（等价于 Pillow 的 `Image.draft()`），直接把 12 MP 解到 1/8 尺寸再缩放
- HEIC 用 libheif
- 纯 Go（`imaging`）作为 `!cgo` 构建的降级路径

**理由**：纯 Go 全解码 409 ms，是 libjpeg-turbo 的 24 倍；68% 的图片没有缩略图，这段成本会被放大到 65 分钟。

**构建标签**：

| 标签 | 解码实现 | AI 能力 |
|---|---|---|
| `cgo` + `openvino` | libjpeg-turbo + libheif | 完整 |
| `!cgo` 或 `!openvino` | `imaging`（慢 24×） | 返回 503 |

Windows 本地开发默认走 `!cgo` 分支，保证无 gcc 也能编译。

### D4 索引输入：缩略图优先

**决策**：

```
缩略图存在 且 缩略图 mtime >= 原图 mtime
    → 读缩略图（4.7 ms）
否则
    → 解原图（17 ms）→ 顺手写回缩略图缓存
```

**理由**：
- 缩略图 320×240，CLIP 输入 224×224，**零信息损失**（96.7% 短边已 ≥ 224）
- 把 HEIC 的 676 ms 降到 4.7 ms
- 回写缩略图让首次索引顺便补齐缺失的 68%，Web 浏览同时受益；原图反正已解码，边际成本只是编码一张 320 JPEG

**前置依赖**：`ThumbnailService` 需先改造，否则会索引到过期内容。四个缺陷与改造方案见 **§6**，作为 P2.5 独立阶段处理。

### D5 向量存储与检索：SQLite BLOB + 内存暴力余弦

**决策**：向量存 `media_embedding.vector`（BLOB，float32 小端），检索时把当前 `model_id` 的全部向量读进内存，做暴力余弦相似度。

**理由**：
- 14k × 512 × 4 B = **28.7 MB**，常驻内存无压力
- 14k × 512 = 7.2 M 次乘加，Go 标量约 15~30 ms，交互无感
- 10 万条 = 205 MB / ~200 ms，仍可接受
- 引入 HNSW/FAISS 需要额外原生依赖与增量维护复杂度，收益在 10 万条以下不存在

**切换阈值**：超过 200k 条向量时再考虑 HNSW（届时再评估，不在本期范围）。

### D6 文本编码：GPU 实时执行 + LRU 缓存

查询时跑文本塔（RoBERTa-wwm-base，静态 1×64）：GPU FP16 19.4 ms、ORT CPU 52.4 ms。
默认走 GPU（与视觉编码器同一设备），GPU 不可用时退回 CPU —— 两者都在 50 ms 以内，
单条查询完全可接受，不需要预计算或异步。

加一层 LRU 缓存（容量 256）避免重复编码同一查询：检索页常见「改一个字再搜」
「翻页重复请求」，命中率会很高。

---

## 4. 系统架构

```
┌─────────────────────────────────────────────────────────┐
│ 客户端（Web / Android）                                   │
│   · 搜索框 → POST /api/search/semantic                    │
│   · 零推理，纯展示                                        │
└───────────────────────┬─────────────────────────────────┘
                        │ HTTP
┌───────────────────────▼─────────────────────────────────┐
│ Go HTTP Server (Chi)                                    │
│                                                          │
│  api/search.go ──────┐                                   │
│  api/index.go  ──────┤                                   │
│  api/dedup.go  ──────┤（P0 已完成）                        │
│                      │                                   │
│  ┌───────────────────▼──────────────────────────────┐    │
│  │ service                                          │    │
│  │  SearchService   查询编码 + 暴力余弦 + 过滤        │    │
│  │  IndexService    遍历 / 变更检测 / 流水线 / job    │    │
│  │  ThumbnailService 缩略图（含 mtime 校验，已改造）  │    │
│  │  DedupService / HashService（P0）                 │    │
│  └───────────────────┬──────────────────────────────┘    │
│                      │                                   │
│  ┌───────────────────▼──────────────────────────────┐    │
│  │ internal/ai  (CGO)                                │    │
│  │  Encoder   图像/文本 → 向量                        │    │
│  │  Session   封装 ORT session（静态 shape）          │    │
│  │  Decoder   libjpeg-turbo / libheif                │    │
│  └───────────────────┬──────────────────────────────┘    │
└──────────────────────┼──────────────────────────────────┘
                       │
        ┌──────────────┴───────────────┐
        │                              │
┌───────▼────────────┐          ┌──────▼─────────────┐
│ onnxruntime        │          │ SQLite (WAL)        │
│  OpenVINO EP       │          │  media_asset        │
│  (iGPU FP16)       │          │  media_embedding    │
│  CPU EP (FP32)     │          │  job / file_hash …  │
└────────────────────┘          └─────────────────────┘
```

---

## 5. 数据模型

P0 已建好 `media_asset` / `media_embedding`，本期只需少量增补。

```sql
-- 已存在（P0 建表）
media_asset(
  id, path UNIQUE, mime, size, width, height, mtime,
  bucket, index_state, last_indexed_at, change_token
)
media_embedding(asset_id, model_id, dim, vector BLOB, created_at)

-- 本期新增
ALTER TABLE media_asset ADD COLUMN source TEXT NOT NULL DEFAULT '';  -- 'thumb' | 'original'
ALTER TABLE media_asset ADD COLUMN error  TEXT NOT NULL DEFAULT '';  -- 失败原因，便于排查
```

**字段语义**：

| 字段 | 说明 |
|---|---|
| `path` | 虚拟路径（`mount/rel`），与 P0 的 `file_hash` 一致，不存宿主绝对路径 |
| `change_token` | `fmt.Sprintf("%d:%d", size, mtime)`。与原值相同则跳过，避免无谓重算 |
| `index_state` | `PENDING` / `DONE` / `FAILED` / `SKIPPED` |
| `model_id` | 如 `cn-clip-vit-b16`。**换模型不删旧向量**，靠 `model_id` 隔离，可平滑切换/回滚 |

**索引判定逻辑**：

```
不需要索引  ⇔  存在 media_embedding(asset_id, 当前model_id)
                且 media_asset.change_token == 当前 (size:mtime)
```

---

## 6. 缩略图子系统（已改造，`34d8f2e`）

语义索引依赖缩略图，但现有缩略图实现有正确性缺陷，**必须先修再谈复用**。

### 6.1 现状：请求路径上的同步懒生成

`GET /api/files/thumbnail?path=...` → `FileHandler.Thumbnail` → `ThumbnailService.GetThumbnail()`：

```go
cachePath := s.cachePath(filePath)
if _, err := os.Stat(cachePath); err == nil {
    return cachePath, nil                      // 命中：只判断文件存在
}
return s.generateImageThumb(filePath, cachePath)  // 未命中：当场生成
```

`generateImageThumb` = `imaging.Open`（**纯 Go 全分辨率解码**）+ `imaging.Fit(320,320,Lanczos)` + JPEG q80。

没有后台任务、没有预热、没有失效、没有 mtime 校验。**覆盖率等于"Web 上翻过的比例"**。

### 6.2 实测覆盖情况

| 项 | 数值 |
|---|---|
| 图片总数 | 13,980 |
| 缓存文件数 | 4,519 |
| 实际命中 | 4,508（32.2%） |
| **缺失** | **9,472（67.8%）** |
| 孤儿缓存（源文件已删） | 11 |

### 6.3 四个缺陷

| 编号 | 缺陷 | 后果 | 证据 |
|---|---|---|---|
| T1 | **无失效逻辑**。`Delete`/`Rename`/`Move` 只调 `invalidateFingerprints` 清内容指纹，不动缩略图 | 删除后缓存泄漏；同路径覆盖上传后**返回旧图** | 已发现 11 个孤儿缓存 |
| T2 | **无 mtime 校验**。只判断 `os.Stat` 是否成功 | 同路径换内容 → 显示/索引错误内容 | 代码直读 |
| T3 | **缓存键用路径下划线替换**：`a_b.jpg` 与 `a/b.jpg` 都映射为 `a_b.jpg.jpg` | 潜在碰撞覆盖 | 当前 0 碰撞，但 13,745/13,980 个文件名含 `_`，纯属路径结构侥幸 |
| T4 | **纯 Go 解码慢**：`image/jpeg` 全解码 12 MP 需 ~380 ms | 单张 530 ms，补齐 9,472 张需 83 分钟 | 见 §2.2 |

> 附带排查：路径扁平化后最长 145 字节（均值 65），**未触及 255 字节文件名上限**。但换成深层路径仍有风险，哈希键可一并消除。

### 6.4 改造方案

**（1）缓存键改为路径哈希**

```
key = hex(sha1(virtualPath))[:20] + ".jpg"
```

- 消除 T3 的碰撞与长度风险
- **破坏性变更**：现有 4,519 个旧键文件成为孤儿，一次性清理后重新生成（约 2 分钟，可接受）

**（2）加 mtime 校验，消除 T2**

```
if thumbMtime >= srcMtime  → 命中
else                       → 重新生成并覆盖
```

代价是每次请求多一次 `os.Stat`（亚毫秒），可接受。

**（3）补失效，消除 T1**

`FileHandler.Delete/Rename/Move` 增加 `thumbSvc.Invalidate(virtualPath)`。改名/移动要同时清源路径与目标路径的缓存。

**（4）解码器换成 libjpeg-turbo + libheif，消除 T4**

与 §3 D3 共用同一个解码器（P1 已因 CLIP 索引引入），**边际成本为零**，不引入 ffmpeg 等额外依赖。

### 6.5 为什么不用 ffmpeg

实测对比（40 张真实照片，12 MP）：

| 方式 | 单张 | 补齐 9,472 张 |
|---|---|---|
| 现有 Go `imaging` | 530 ms | 83 分钟 |
| ffmpeg 单进程串行 | 321 ms | 85 分钟 |
| ffmpeg 4 进程并行 | 85.7 ms（摊薄） | 13.5 分钟 |
| **libjpeg 进程内（DCT 降采样）** | **45.1 ms** | — |
| **libjpeg 进程内 4 线程** | **12.8 ms** | **≈ 2 分钟** |

ffmpeg 单进程 321 ms 里进程启动与解复用器初始化占了大头；4 进程能压到 85.7 ms，但仍比进程内调用慢 6.7 倍，且要额外管理子进程、解析退出码、耦合镜像里的 ffmpeg 版本。**不值得。**

### 6.6 批量补齐（Backfill）

新增独立后台任务，复用 P0 的 `job` 表（`type = "thumb_backfill"`）：

```
POST /api/thumb/backfill   { "force": false }
POST /api/thumb/cancel
GET  /api/thumb/status     → { total, done, missing, orphan, running }
```

流水线：`walk → 过滤（无缓存 / mtime 过期 / force 全量）→ N 个 worker 生成 → 写盘 → 进度上报`

并发度：`min(4, NumCPU)`。实测 4 线程摊薄 12.8 ms，已接近 IO 瓶颈，再加无收益。

### 6.7 预生成 vs 索引时回写的取舍

两条路线的总耗时几乎一样：

| 路线 | 步骤 | 耗时 |
|---|---|---|
| **预生成** | backfill 9,472 张（2 min）+ 索引全走缩略图 13,980 × 116 ms（27 min） | **29 min** |
| **索引时回写** | 4,508 走缩略图 × 116 ms + 9,472 解原图 × 138 ms（含缩略图编码） | **30.5 min** |

差 1.5 分钟，可以忽略。**所以预生成的价值不在提速，而在：**

1. **Web 浏览立刻全部可用**——现在 67.8% 的图点开要等 530 ms
2. **解耦**——缩略图是 Web 功能，不该被 AI 索引绑架；AI 关闭时也该能补齐
3. **重建索引成本恒定**——换模型后不用再碰原图，恒 29 分钟

**结论：两条都做。** Backfill 作为独立任务（用户可随时触发），索引侧仍保留"缺失则解原图并回写"的兜底，保证索引不依赖 backfill 是否跑过。

---

## 7. 索引流水线

### 7.1 阶段

```
[1] 遍历（walker）
      mount 列表 → 递归 walk → 扩展名过滤 → 排除路径过滤
      ↓
[2] 变更检测
      读 media_asset，算 change_token，与库中比对
      已有当前 model_id 向量且 token 未变 → 跳过
      ↓
[3] 取图（decoder，1 个 goroutine）
      thumb 存在且新鲜 → 解缩略图
      否则 → 解原图 + 回写缩略图
      → 输出 224×224×3 float32（已归一化，CHW 布局）
      ↓
[4] 推理（encoder，1 个 goroutine，GPU 串行）
      模型静态 1x3x224x224，逐张推理（不要改动态 batch，见 §2.5）
      → 512 维向量，L2 归一化
      ↓
[5] 落库（writer，批量 64 条一个事务）
      更新 media_asset.index_state / change_token / last_indexed_at
      insert media_embedding
      ↓
[6] 进度上报（每 50 张或每 2 秒）
      更新 job.done / job.message
```

### 7.2 预处理规范（必须严格对齐训练分布）

```
输入原图 / 缩略图（任意尺寸，RGB）
  → 直接缩放到 224×224（BICUBIC，宽高比会失真）
  → 像素值 /255
  → 逐通道减均值 [0.48145466, 0.4578275, 0.40821073]
  → 逐通道除标准差 [0.26862954, 0.26130258, 0.27577711]
  → 转 CHW，float32，连续内存
```

**注意：是直接拉伸，不是「短边缩放 + 中心裁剪」。** 这一点与 OpenAI CLIP 的惯例相反，
容易想当然写错，所以单独说明。

依据是随权重发布的 `preprocessor_config.json`：

```json
{ "do_center_crop": false, "do_resize": true, "size": {"height": 224, "width": 224},
  "resample": 3, "image_mean": [...], "image_std": [...] }
```

`do_center_crop: false` + `size` 直接给 224×224，意味着 `resize` 到 224×224 后不裁剪 ——
也就是拉伸。`resample: 3` 是 BICUBIC。

**实测 A/B**（`scripts/bench/ab_preproc.py`，用官方仓库自带的 `festival.jpg`，
3 条贴合画面的中文描述 + 3 条明显无关的，看「贴合 - 无关」的间隔）：

| 预处理 | 贴合均值 | 无关均值 | margin |
|---|---|---|---|
| **官方拉伸** | 0.3539 | 0.2814 | **+0.0725** |
| 短边缩放 + 中心裁剪 | 0.3381 | 0.2712 | +0.0669 |

两种方案的图像向量余弦 0.974 —— 差异不算灾难，但官方拉伸稳定占优，且与配置文件一致。
所以按官方来。若后续 P6 校准阶段发现宽高比失真确实影响某些类别的检索，
可以再拿真实查询做人工评测推翻这个结论，但**不要凭直觉改**。

**缩略图路径无需特殊处理**：现有缩略图是 `Fit` 等比缩放的，320×240 的缩略图
和它的 4000×3000 原图宽高比一致，拉伸到 224×224 得到同样的几何结果（只是重采样
起点分辨率不同）。所以索引缩略图和索引原图在几何上等价，这正是 §6.7 敢用缩略图的前提。

### 7.3 并发模型

**关键：不要盲目加线程。** 解码只占 4.7~17 ms，推理占 121 ms，解码侧并行带来的
收益上限只有 10% 左右，而两者抢同一颗 die 的功耗/带宽预算，实测加解码线程后
端到端反而变慢。单解码 goroutine 就够。

推荐配置：

| 阶段 | goroutine 数 | 说明 |
|---|---|---|
| walker | 1 | 只做 stat，IO 轻 |
| decoder | 1（可配 2） | 4.7~17 ms，远小于推理 121 ms，无需并行 |
| encoder | **1** | GPU 串行，batch=1 已饱和（8.3 vs batch=16 的 9.7 img/s） |
| writer | 1 | 批量提交 |

用**有界 channel**（容量 8）串起来，形成背压：推理慢时自动阻塞解码，不会堆积内存。

> 若后续换成纯 Go 解码（409 ms），decoder 需要提到 2~3 个 goroutine。已实测：此时解码成为瓶颈，并行有效。

### 7.4 任务管理

复用 P0 的 `job` 表与状态机（`PENDING/RUNNING/DONE/FAILED/CANCELED`）：

- 同一时刻只允许一个 `index` 任务（与 dedup 的 `StartScan` 同构）
- 进程重启时 `FailStaleJobs` 把残留的 RUNNING 标为 FAILED（已实现）
- 取消：context cancel，流水线在每张图之间检查
- 续跑：中断后重新发起 `scan(full=false)`，已完成的不再重算

### 7.5 全量 vs 增量

| 模式 | 触发 | 行为 |
|---|---|---|
| 增量 | 默认 `scan` | 只处理 `change_token` 变化或无向量的资产 |
| 全量 | `scan {full:true}` | 忽略 `change_token`，全部重算（换模型后清理旧向量时用） |

### 7.6 资源控制

索引会长时间占用 CPU 与 iGPU。提供两个开关：

- `index.throttle_ms`：每张图后 sleep，默认 0
- `index.batch_pause`：每 N 张暂停 M ms

Web 请求与索引共存时，GPU 推理会抢占；实测单张 121 ms 的延迟抖动对检索（同样要过 GPU）影响可感知。**建议索引任务在低峰期执行**，前端提供"夜间索引"选项。

---

## 8. 检索流程

### 8.1 文搜图

```
query "海边日落"
  → 分词（HuggingFace tokenizer，Go 侧实现 BPE）
  → 文本塔推理（GPU FP16 19.4 ms；GPU 不可用时退回 CPU 52 ms）
  → 512 维查询向量（L2 归一化）
  → 与内存中所有 model_id 匹配的向量做点积（= 余弦，因为都已归一化）
  → 排序 → 取 top-K → 过滤 min_score → 校验文件仍存在
  → 返回 path + score + 缩略图 URL
```

### 8.2 以图搜图

```
源图片 path
  → 取它自己的 embedding（查 media_embedding）
  → 同 7.1 的检索段
  → 排除自身
```

### 8.3 分数校准

CLIP 的图文余弦分数分布与视觉相似度不同：

- 图文匹配通常在 **0.20 ~ 0.35**，无关约 0.10 以下
- 图图相似通常更高，**0.85+** 才算"同款"

因此 `min_score` 需要**按查询类型分别配置**，并在 P5 阶段用真实库标注一批样本校准。默认值先给：文搜图 `0.18`，图搜图 `0.75`。

### 8.4 内存索引

首次检索时把向量全量加载进内存（`map[modelID]*VectorIndex`），之后增量维护：

- 索引任务每写入一批 → 追加到内存索引
- 进程启动后**后台预热**（`SearchService.Warmup`），不阻塞启动；预热完成前到来的
  检索请求会走同一把锁做同步加载，不会读到半截索引

---

## 9. HTTP API

全部沿用 P0 的 `model.APIResponse{OK, Data, Error}` 结构。AI 不可用时统一返回 **503**（与 dedup 关闭时一致）。

### 9.1 语义检索

```
POST /api/search/semantic
Content-Type: application/json

{ "query": "海边日落", "limit": 50, "min_score": 0.18 }

200 {
  "ok": true,
  "data": {
    "items": [
      { "path": "照片/2024/IMG_1234.jpg", "score": 0.312, "thumb_url": "/api/thumb?path=..." }
    ],
    "took_ms": 18,
    "total_candidates": 13978
  }
}
```

### 9.2 以图搜图

```
POST /api/search/similar
{ "path": "照片/2024/IMG_1234.jpg", "limit": 50, "min_score": 0.75 }
```

### 9.3 索引管理

```
GET  /api/index/status
200 { "ok":true, "data":{
      "model_id":"cn-clip-vit-b16", "device":"GPU",
      "total":13978, "indexed":13978, "pending":0, "failed":0,
      "running":null } }

POST /api/index/scan      { "full": false }   → { "ok":true, "data":{"job_id":"..."} }
POST /api/index/cancel    { "job_id":"..." }
GET  /api/index/jobs                          → 复用 P0 的 job 列表
```

### 9.4 自检

```
GET /api/index/health
200 { "ok":true, "data":{
      "backend":"onnxruntime-openvino-1.24.1",
      "device":"GPU", "gpu_name":"Intel UHD Graphics",
      "image_model_loaded":true, "text_model_loaded":true,
      "warmup_ms":3700, "dim":512,
      "selftest":{"image_cos":0.99999,"text_cos":0.99999,"pass":true} } }
```

`selftest` 是启动时用 `golden.npz` 做的自检。**它是必需的，不是可选的**：
GPU 路径出问题时不会报错，只会给出错向量（§2.5），没有这一项就无法在部署后
发现环境变化（换驱动、换容器基底、EP 版本漂移）导致的质量退化。

---

## 10. 配置

在 `config.json` 中新增 `ai` 与 `thumbnail` 两段（P0 已加的 `dedup` 段保持不动）：

```json
{
  "thumbnail": {
    "max_size": 320,
    "jpeg_quality": 80,
    "key": "sha1",
    "validate_mtime": true,
    "backfill_workers": 4
  },
  "ai": {
    "enabled": true,
    "model_dir": "./data/models/cn-clip-vit-b16",
    "model_id": "cn-clip-vit-b16",
    "device": "GPU",
    "precision": "FP16",
    "batch_size": 1,
    "decode_workers": 1,
    "index_workers": 1,
    "throttle_ms": 0,
    "prefer_thumbnail": true,
    "writeback_thumbnail": true,
    "selftest": true,
    "search": {
      "default_limit": 50,
      "text_min_score": 0.18,
      "image_min_score": 0.75
    },
    "extensions": [".jpg", ".jpeg", ".png", ".webp", ".bmp", ".gif", ".heic", ".heif"]
  }
}
```

- `device` 支持 `GPU` / `CPU`，探测失败自动降级到 `CPU` 并打 WARN（慢 6 倍，必须可见）
- `precision` 仅 GPU 生效，`FP16` 实测余弦 0.99999，无精度顾虑
- `batch_size` 恒为 1（模型是静态 shape，改了会出错，见 §2.5）
- `selftest` 打开时启动会用 `golden.npz` 对拍，失败则 `enabled` 自动置 false 并记录原因
- `thumbnail.key` 仅 `sha1` 一个取值（保留字段是为了后续可扩展，旧值 `path` 不再支持）
- `thumbnail.max_size` / `jpeg_quality` **不要随意调**：索引侧假设短边 ≥ 224

---

## 11. 构建与部署

### 11.1 镜像改造（已落地）

原 `server/Dockerfile.server` 是 **alpine + `CGO_ENABLED=0`**，无法承载
onnxruntime / OpenVINO / libjpeg-turbo（均为 glibc 构建）。现已改为 Debian 基底，
实际文件见 `server/Dockerfile.server`，关键差异如下：

| 项 | 做法 | 原因 |
|---|---|---|
| 基底 | `golang:1.22-bookworm` → `debian:bookworm-slim` | 只有 glibc 基底能加载 ORT/OpenVINO |
| 解码 | `-tags libjpeg` + `libjpeg62-turbo-dev` | DCT 降采样，12MP JPEG 17ms vs 380ms |
| 运行库 | 独立 `ortlibs` 阶段 `pip install onnxruntime-openvino` 后拷 `.so` | 构建期确定版本，运行镜像不带 Python |
| iGPU 驱动 | 安装失败只告警，不失败构建 | 降级 CPU 仍可用，`/api/index/health` 暴露 `device` |

```dockerfile
FROM python:3.12-slim AS ortlibs
ARG ORT_VERSION=1.24.1
RUN pip install --no-cache-dir --target /ortpkg onnxruntime-openvino==${ORT_VERSION} \
 && mkdir -p /opt/ort && cp -a /ortpkg/onnxruntime/capi/*.so* /opt/ort/

FROM golang:1.22-bookworm AS builder
RUN apt-get update && apt-get install -y --no-install-recommends libjpeg62-turbo-dev
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ .
RUN CGO_ENABLED=1 go build -tags libjpeg -ldflags="-s -w" -o mini-nas-server ./cmd/server

FROM debian:bookworm-slim
COPY --from=ortlibs /opt/ort /opt/ort
ENV LD_LIBRARY_PATH=/opt/ort
```

> 注意：**不要同时装 `onnxruntime` 和 `onnxruntime-openvino`**。两者会把不同版本的
> `libonnxruntime.so` 装进同一个目录（实测出现 1.31.1 与 1.24.1 并存），
> 运行时到底加载哪个取决于 `LD_LIBRARY_PATH` 顺序，属于随机故障源。
> 只用 `onnxruntime-openvino`，它自带主库和 EP。

### 11.2 docker-compose 增补

iGPU 直通需要设备与组：

```yaml
services:
  server:
    devices:
      - /dev/dri:/dev/dri
    group_add:
      - render          # 宿主的 render 组 GID 需与容器一致，必要时写数字 GID
    volumes:
      - ./data:/data
      - ./models:/data/models:ro   # 模型只读挂载，不进镜像
      - /:/host
    environment:
      - TZ=Asia/Shanghai
      - HOST_ROOT=/host
      - LD_LIBRARY_PATH=/opt/ort
```

**宿主前置条件**（目标机已完成）：

```bash
sudo apt-get install -y intel-opencl-icd libze-intel-gpu1
sudo usermod -aG render $USER     # 生效需重新登录
ls /dev/dri/                       # 应有 card1 与 renderD128
```

### 11.3 模型产物

产物目录 `server/models/cn-clip-vit-b16/`：

| 文件 | 大小 | 入库 | 说明 |
|---|---|---|---|
| `image_encoder.onnx` | 328.9 MB | ❌ | 静态 1×3×224×224 → 1×512 |
| `text_encoder.onnx` | 389.5 MB | ❌ | 静态 1×64 → 1×512 |
| `vocab.txt` | 107 KB | ✅ | 21130 tokens，行号即 token id |
| `meta.json` | 800 B | ✅ | 维度/输入名/归一化参数/静态 shape 声明 |
| `golden.npz` | 554 KB | ✅ | 固定输入 + PyTorch 参考输出（模型级对拍） |
| `preproc_golden.npz` | 168 KB | ✅ | 合成源图 + 期望张量（预处理级对拍） |
| `verify.json` | 1.3 KB | ✅ | 导出校验报告 |

**只把 ONNX 权重排除在版本控制外**（`.gitignore` 规则 `server/models/**/*.onnx`），
其余约 830 KB 的夹具照常入库。

理由：两份 ONNX 合计 718 MB。GitHub 免费 LFS 只有 1 GB 存储 + 1 GB/月流量，
占 72% 存储，且每次全新 clone 就烧掉约 718 MB 月流量；走 Release 附件又需要
人工维护。而导出过程完全确定（固定 seed、固定预处理、固定 opset），
用一条命令就能复现，没有必须入库的理由。

但**夹具必须入库**，否则新 clone 下来跑不了 Go 侧对拍测试，`golden.npz`
存在的意义（让 Go 不依赖 Python 就能自证正确）就没了。

**复现**（约 4 分钟，其中下载权重约 100 秒）：

```bash
scripts/setup_model.sh
# 产物落到 server/models/cn-clip-vit-b16/
```

脚本做四件事：从 ModelScope 拉 PyTorch 权重 → 导出静态 shape ONNX → 跑四段校验
→ 生成预处理黄金样本。可用环境变量覆盖：`SRC_DIR`（权重缓存）、
`OUT_DIR`（产物目录）、`PY`（解释器）、`MODEL_ID`（换模型）。

**导出是逐字节可复现的**（固定 seed + 固定 opset + `do_constant_folding`）。实测两次
独立导出 md5 完全一致，所以「不入库」不会带来版本漂移风险：

```
158ab9ee5d83d511a8e6b4d74b5946a0  image_encoder.onnx
78457542dcac33cce82810ac944170ff  text_encoder.onnx
f642a1682a637cffd0d57e445b820b72  golden.npz
3b5b76c4aef48ecf8cb3abaafe960f09  vocab.txt
```

> 注意：md5 一致的前提是 **torch / transformers 版本一致**。换版本后 ONNX 图结构可能
> 变化（算子融合策略、opset 实现细节），届时 md5 会变但余弦校验仍应通过 ——
> 所以**以 `verify.json` 的余弦为准，不要以 md5 为准**。上面这组 md5 仅作环境一致性参考。

**接口契约**（Go 侧必须按此实现，`meta.json` 会带同样的字段）：

| 项 | 值 |
|---|---|
| 视觉输入 | `pixel_values`，float32，`[1, 3, 224, 224]`，CHW，RGB |
| 视觉输出 | `image_embeds`，float32，`[1, 512]`，**未归一化** |
| 文本输入 | `input_ids` + `attention_mask`，int64，`[1, 64]` |
| 文本输出 | `text_embeds`，float32，`[1, 512]`，**未归一化** |
| 归一化 | L2 在 Go 侧做，模型输出保持原始尺度 |
| 预处理 | **直接拉伸到 224×224（BICUBIC）** → /255 → 减均值/除标准差 → CHW |
| mean | `[0.48145466, 0.4578275, 0.40821073]` |
| std | `[0.26862954, 0.26130258, 0.27577711]` |
| 文本长度 | 固定 64，`padding="max_length"`，超长截断 |

> 预处理是**拉伸**不是裁剪，理由与实测见 §7.2。这一条最容易写错。

**两个对拍夹具的用法**（Go 侧 `-selftest` 与单测都应覆盖）：

```go
// 1) 预处理级：验证 resize/归一化/CHW 排布对不对
pf := loadNPZ("server/models/cn-clip-vit-b16/preproc_golden.npz")
src, _ := png.Decode(bytes.NewReader(pf.SourcePNG))       // 641x427 合成图
got := preprocess(src)                                     // 你的实现
require(cosine(got, pf.Expected) > 0.999)                  // 插值实现不同，允许微小差异

// 2) 模型级：验证推理链路整体对不对
gf := loadNPZ("server/models/cn-clip-vit-b16/golden.npz")
out := imageEncoder(gf.PixelValues)
require(cosine(out, gf.ImageEmbeds) > 0.999)               // 应与 CPU 参考几乎完全一致
```

两级夹具的分工：**预处理级定位问题，模型级判定通过**。只跑模型级的话，
预处理错了只会看到余弦偏低，不知道该改哪儿；只跑预处理级的话，
ORT 绑定错了（比如输入名写错、shape 传错）反而发现不了。

`preproc_golden.npz` 里的期望张量由官方 `AutoImageProcessor` 生成，
并用配置文件声明的参数手算复核过（`max|diff| = 0`），可以放心当基准。

**权重来源**（HF 在目标机不可达，hf-mirror 大文件会卡）：

```bash
ID=AI-ModelScope/chinese-clip-vit-base-patch16
curl -L "https://www.modelscope.cn/api/v1/models/$ID/repo?Revision=master&FilePath=config.json"
# 完整文件列表：config.json / preprocessor_config.json / vocab.txt / pytorch_model.bin
```

目标机上已有一份备份，换机器部署时可省掉下载：
`/mnt/nas/dev/mini-nas/server/models/cn-clip-vit-b16/`（ONNX 产物）
`/mnt/nas/dev/mini-nas/.models-src/`（PyTorch 原始权重，719 MB）

### 11.4 本地开发（Windows，无 gcc）

```bash
go build ./...                              # 默认 !cgo，AI 返回 503，其余功能正常
CGO_ENABLED=1 go build -tags libjpeg ./cmd/server   # 需 gcc + jpeglib.h + onnxruntime，仅 Linux
```

`make build-server` / `make build-server-cgo` 分别对应上面两条。改完 `go.mod`
相关代码后必须用 `make tidy`（内部固定 `GOOS=linux CGO_ENABLED=1`）整理依赖，
否则 `onnxruntime_go` 只在 cgo 文件里被 import，会被当成未使用依赖删掉。

---

## 12. 实施计划

| 阶段 | 内容 | 交付物 | 验收标准 |
|---|---|---|---|
| **P0** | 去重（MD5 / 扫描 / 合并） | ✅ 已完成（`eb298ad`） | 本地 9 个场景全部通过 |
| **P0.1** | 全新部署默认挂载点修复 | ✅ 已完成（`d73a360`） | `e2e_fresh_deploy.sh` 通过 |
| **P1** | 模型导出与入库 | ✅ 导出已完成并校验 | ONNX 输出与 PyTorch 余弦 > 0.999（实测 1.00000000 / 0.99999988） |
| **P2.5** | 缩略图子系统改造（§6） | ✅ 已完成（`34d8f2e`） | 覆盖率 100%；覆盖后不再返回旧图；backfill 可跑 |
| **P2** | `internal/ai` 推理内核 | ✅ 已完成 | 分词器与 golden 逐 token 一致；预处理对拍 cos=1.00000000 |
| **P3** | 索引流水线 | ✅ 已完成 | `IndexService` + `/api/index/*`，可取消、可续跑、含幽灵记录清理 |
| **P4** | 检索 API | ✅ 已完成 | `/api/search/semantic`、`/api/search/similar` |
| **P5** | 前端 | ✅ 已完成 | Web 智能检索面板 + 文件右键「找相似图片」 |
| **P6** | 校准与调优 | 待做 | 真实库标注样本校准阈值 |

### 12.1 实现偏差记录（P2~P5）

落地时有几处与上文设计不同，均有明确原因，改动前请先读：

| 项 | 设计 | 实现 | 原因 |
|---|---|---|---|
| ORT 绑定 | 手写 CGO 封装 `OrtApi` 函数表 | `github.com/yalue/onnxruntime_go`（dlopen 动态加载） | `OrtApi` 是函数指针表，手写要精确对齐字段顺序，跨版本极易错位；该库用 dlopen，构建期不需要头文件，运行期只需 `.so`。其 API 版本为 22，要求 ORT ≥ 1.22，镜像固定 1.24.1 |
| libjpeg 启用方式 | `cgo` 标签 | `cgo && libjpeg` 双标签，Dockerfile 显式 `-tags libjpeg` | 避免任何 cgo 构建都被迫依赖 `jpeglib.h`；缺标签时自动退回标准库解码（慢 24 倍但结果正确） |
| HEIC（F6） | libheif 接入 | **未实现**，`.heic/.heif` 不参与索引 | 目标库 HEIC 为 0 张；libheif 绑定无法在开发机验证，宁可留空也不上线未测代码。配置项保留，后续接入不影响调用方 |
| `/api/index/status` | AI 不可用时 503 | 200 + `available:false` + `reason` | 状态接口的职责就是解释「为什么不可用」，503 会让前端无从展示原因。`scan`/`health`/`search` 仍是 503 |
| 以图搜图源图未索引 | 直接报错 | 现场编码并落库 | 用户常对刚上传的图「以图搜图」，此时索引任务可能还没跑到 |
| 幽灵记录 | 检索时校验存在性 | 检索校验 + 扫描结束 `PruneAssets` | 只校验会让库里的死记录越积越多 |
| 模型目录解析 | 配置里写死路径 | 留空 → `<data_dir>/models/<model_id>`；相对路径按「工作目录 → 数据目录」两级探测 | 本地 `make dev-server` 的 cwd 是 `server/`（模型在 `server/models`），容器里 cwd 是 `/app` 而模型挂在 `/data/models`，同一份 `config.json` 要两处都能命中。解析结果只进局部变量、不写回配置，避免容器把 `/data/...` 固化进宿主机文件 |
| `PruneAssets` 范围 | 按 `keep` 集合全库清理 | 增加 `scope` 参数，只清理本次实际走到的挂载点 | 挂载点临时掉线时，全库清理会把整个挂载的文件记录误判为已删除 |

### P1 交付状态（已完成）

产物在 `server/models/cn-clip-vit-b16/`，四段校验全绿：

```
[PASS] image: cos min=1.00000000  deterministic=True
[PASS] text:  cos min=0.99999988  deterministic=True
[PASS] semantics: 猫/小猫 0.9174 > 猫/汽车 0.5508 ；日落/黄昏落日 0.9629 > 日落/打印机 0.6343
[PASS] GPU FP16 image: cos=0.99999517
[PASS] GPU FP16 text:  cos=0.99999702
```

`golden.npz`（模型级）与 `preproc_golden.npz`（预处理级）已入库，
Go 侧做完预处理和推理后可以直接对拍，**不需要 Python 环境**：

```go
// 预处理级：定位问题
pf := loadNPZ("server/models/cn-clip-vit-b16/preproc_golden.npz")
src, _ := png.Decode(bytes.NewReader(pf.SourcePNG))
require(cosine(preprocess(src), pf.Expected) > 0.999)

// 模型级：判定通过
gf := loadNPZ("server/models/cn-clip-vit-b16/golden.npz")
require(cosine(encoder.Image(gf.PixelValues), gf.ImageEmbeds) > 0.999)
```

### P2.5 拆分（缩略图）—— ✅ 已完成

依赖 P2 的 `decode_cgo.go`（libjpeg-turbo + libheif），不额外引入依赖。

1. 缓存键改 `sha1(virtualPath)[:20]`，清理旧的 4,519 个路径键文件
2. `GetThumbnail` 加 mtime 校验（`thumbMtime >= srcMtime`）
3. `Delete/Rename/Move` 增加 `thumbSvc.Invalidate()`，顺带清理现存 11 个孤儿
4. 解码器从 `imaging` 切到 libjpeg-turbo，生成规格保持 `320 框内 + JPEG q80` 不变
5. `Backfill` 任务（`job.type = "thumb_backfill"`）+ `/api/thumb/*`
6. 补 HEIC 解码（与索引共用）

> 生成规格不变是有意为之：现有 4,508 个可用缩略图短边 96.7% ≥ 224，
> 换键后重新生成时保持同一规格，索引侧无需区分新旧。

### P2 拆分（最关键，风险最高）

1. `internal/ai/ort.go` — CGO 绑定 onnxruntime C API：
   `OrtGetApiBase` → `CreateEnv` / `CreateSessionOptions` /
   `SessionOptionsAppendExecutionProvider_OpenVINO`（`device_type=GPU` + `precision=FP16`）/
   `CreateSession` / `CreateTensorWithDataAsOrtValue` / `Run`。
   注意 `OrtApi` 是函数指针表，CGO 里要用 `static` 内联包装函数转发，不能直接跨语言取结构体成员
2. `internal/ai/encoder.go` — 图像预处理（**直接拉伸到 224×224** + 归一化 + CHW）与后处理（L2 归一化）
3. `internal/ai/decode_cgo.go` — libjpeg-turbo（`scale_denom`）+ libheif
4. `internal/ai/stub.go` — `!cgo` 空实现，返回 `ErrUnavailable`
5. `internal/ai/selftest.go` — 启动自检与预热（**含两级夹具对拍**）
6. **`internal/ai/tokenizer.go` — BERT WordPiece 分词器（Go 实现）**

> **静态 shape 是硬约束。** 会话创建后输入维度固定为 `1x3x224x224` 和 `1x64`。
> 任何"顺手支持一下动态 batch"的改动都会把 GPU 输出打坏（§2.5），
> 且不会报错。批处理要在 Go 侧循环调用，不要改模型。

> **预处理是拉伸不是裁剪。** §7.2 有实测依据。实现完先用 `preproc_golden.npz`
> 单独验证这一步，再去接 ORT —— 否则预处理和推理混在一起，出问题分不清是谁的错。

> **第 6 项容易被低估。** 导出脚本只产出 `vocab.txt`（一行一个 token，行号即 id），
> Go 侧需要自己实现：BasicTokenizer（含中文按字切分、小写化、标点切分）+ WordPiece
> 贪心最长匹配 + `[CLS]`/`[SEP]` 包装 + `attention_mask` 生成 + **padding 到 64**。
> 约 200~300 行，可参考 `sugarme/tokenizer` 的实现方式，但建议手写以精确对齐
> `BertTokenizer(do_lower_case=True)` 的行为。
> **验收**：同一批中文 query，Go 输出的 `input_ids` 与 Python
> `ChineseCLIPProcessor` 完全一致（逐 token 比对）。`golden.npz` 里的
> `text_input_ids` / `sem_input_ids` 可直接当测试向量。
>
> 注意 tokenizer 的输入是**已 pad 到 64** 的 `[1,64]`，pad 位填 `[PAD]`(id=0)、
> `attention_mask` 填 0。中文按字切分意味着「一只猫」→ 3 个 token 而非 1 个，
> 别按空格切。

**分词测试向量**（实测，直接当单测期望值）：

```
特殊 token: [PAD]=0  [UNK]=100  [CLS]=101  [SEP]=102  [MASK]=103

"一只猫坐在窗台上"  (10 tokens)
  ids    = [101, 671, 1372, 4344, 1777, 1762, 4970, 1378, 677, 102]
  tokens = [[CLS], 一, 只, 猫, 坐, 在, 窗, 台, 上, [SEP]]

"海边日落"  (6 tokens)
  ids    = [101, 3862, 6804, 3189, 5862, 102]
  tokens = [[CLS], 海, 边, 日, 落, [SEP]]

"红色汽车"  (6 tokens)
  ids    = [101, 5273, 5682, 3749, 6756, 102]
  tokens = [[CLS], 红, 色, 汽, 车, [SEP]]

"city skyline at night"  (7 tokens，注意 WordPiece 会把 skyline 拆开)
  ids    = [101, 8869, 10122, 8762, 8243, 10036, 102]
  tokens = [[CLS], city, sky, ##line, at, night, [SEP]]
```

最后一条专门用来验 WordPiece 贪心最长匹配：`skyline` 要拆成 `sky` + `##line`
而不是落进 `[UNK]`。只测纯中文的话这个 bug 发现不了。

### P3 拆分

1. 迁移：`media_asset` 加 `source` / `error` 字段
2. `IndexService.Scan` — 复用 `DedupService.StartScan` 的任务管理骨架
3. 变更检测 + 缩略图优先 + 回写
4. 批量写入与进度上报
5. `/api/index/*` handler（503 门控与 dedup 一致）

---

## 13. 风险与未决项

| 编号 | 风险 | 影响 | 缓解 |
|---|---|---|---|
| R1 | onnxruntime C API 的 Go 封装工作量被低估（`OrtApi` 是函数指针表） | P2 延期 | 先做最小闭环（单图推理 + golden 对拍），跑通再扩；用 C 静态内联函数转发 |
| R2 | 容器内 GPU 直通失败 | 退回 CPU，慢 6 倍（14k 约 3 小时） | 裸机已验证；`/api/index/health` 必须暴露 `device` |
| R3 | Debian 上 `libheif1` 编译/运行异常 | HEIC 索引失败 | 单张失败记 `error` 字段跳过，不中断任务；必要时换 `heif-convert` 子进程 |
| R4 | 缩略图子系统四个缺陷 T1~T4（§6.3） | 索引到过期内容 / 显示旧图 / 缓存泄漏 | ✅ 已在 P2.5 修复 |
| R5 | ViT-L/14 吞吐**未实测**（HF 不可达） | 无法确认是否值得换大模型 | 按 FLOPs 推算约 500 ms/img，已足以否决 |
| R6 | 首次加载 3.7~5.2 s（含 GPU 编译） | 首次检索卡顿 | 启动后异步预热，`/api/index/health` 暴露 `warmup_ms` |
| R7 | `MountService.AddDefaultMount` 是死代码 | 全新部署零挂载，首次上传必失败 | ✅ 已修（`d73a360`） |
| R8 | 索引期间 Web 卡顿 | 体验差 | `throttle_ms` + 夜间任务；检索与索引共用 GPU 需排队 |
| R9 | 纯 Go 解码路径（409 ms）未做并发优化 | `!cgo` 构建下索引极慢 | 仅用于开发，不做性能承诺；文档标注 |
| R10 | Go 侧 BERT 分词器与 Python 不一致 | 查询向量偏移，检索质量下降 | 用 `golden.npz` 的 `text_input_ids` 做 token 级对拍，纳入 P2 验收 |
| R11 | **OpenVINO EP 动态 shape 静默输出错向量**（§2.5） | 检索整体失真且无任何报错 | 模型强制静态 shape；导出脚本内置 GPU 对拍；P2 自检也必须跑 golden 对拍 |
| R12 | 缩略图缓存键切换为哈希是破坏性变更 | 现有 4,508 个有效缩略图需重建 | ✅ 已随 P2.5 完成，重建约 2 分钟 |
| R13 | 模型 718 MB 超出 GitHub 免费 LFS 配额舒适区（1 GB 存储 / 1 GB 月流量） | 全新 clone 失败或超限 | ✅ 已决策**不入库**，改 `scripts/setup_model.sh` 一键复现（§11.3） |
| R14 | `onnxruntime` 与 `onnxruntime-openvino` 同时安装导致版本混装 | 运行时加载到错误主库，随机故障 | 只装 `onnxruntime-openvino`，Dockerfile 里固定版本 |

---

## 附录 A：基准复现方法

所有数据来自目标机 `ssh.zhangjh.cn`，脚本已入库到 `scripts/bench/`（含 README 与陷阱说明）。

```bash
# 环境（一次性）
sudo apt-get install -y intel-opencl-icd libze-intel-gpu1
sudo usermod -aG render $USER
python3 -m venv /tmp/bench && /tmp/bench/bin/pip install \
  onnxruntime-openvino numpy pillow pillow-heif

# 模型导出 + 四段校验（含 GPU 保真度）
sg render -c "/tmp/bench/bin/python scripts/export_clip_onnx.py \
  --model /tmp/bench/cnclip-b16 --out /tmp/bench/out/cn-clip-vit-b16"

# 只校验（快，不加载 torch）
sg render -c "/tmp/bench/bin/python scripts/export_clip_onnx.py \
  --out /tmp/bench/out/cn-clip-vit-b16 --verify-only"

# 导出产物的性能 + 保真度（CPU/GPU 各配置）
sg render -c "/tmp/bench/bin/python scripts/bench/bench_exported.py \
  --model /tmp/bench/out/cn-clip-vit-b16"

# 动态 shape 的 GPU 错误复现（诊断用，证明 §2.5）
sg render -c "/tmp/bench/bin/python scripts/bench/diag_ovgpu.py"
sg render -c "/tmp/bench/bin/python scripts/bench/diag_ort_ov_static.py"

# 原生 OpenVINO 对照（证明 GPU 本身没问题，坏的是动态 shape）
sg render -c "/tmp/bench/bin/python scripts/bench/bench_ov_native.py"

# 缩略图尺寸分布
/tmp/bench/bin/python scripts/bench/sz.py
```

**两个必须知道的陷阱**：

1. GPU 测试必须经 `sg render -c`，否则 OpenVINO 报 `Device GPU is not available` 并
   静默回退 CPU（表现为 722 ms，极易误判）。
2. **只测速度不测数值的基准等于没测。** 初版预研就因为漏了这一步，把"GPU 返回全零
   向量"当成了"GPU 快 6.9 倍"。所有性能脚本都必须带 `cos_vs_torch` 一列。

## 附录 B：P0 已交付能力（本期依赖）

| 能力 | 位置 | 状态 |
|---|---|---|
| SQLite 持久化 + schema | `internal/store/` | ✅ |
| 内容指纹（partial + full MD5） | `internal/service/hash_service.go` | ✅ |
| 上传时去重（staged write） | `internal/api/files.go` | ✅ |
| 全量扫描 + 分组 | `internal/service/dedup_service.go` | ✅ |
| 硬链接合并 / 移入回收站 | 同上 | ✅ |
| 任务状态机 + 僵尸任务清理 | `internal/store/job.go` | ✅ |
| 8 个去重接口 | `internal/api/dedup.go` | ✅ |
| Windows 虚拟路径归一化 | `internal/store/files.go` `normPath` | ✅ |

**P0 遗留待修**：`AddDefaultMount` 死代码（R7）。
