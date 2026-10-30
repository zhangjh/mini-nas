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
| 推理加速 | **iGPU 可用**，OpenVINO GPU FP16 比 CPU 快 **6.9 倍**（763ms → 111ms） |
| 推理后端 | **OpenVINO C API**（不引入 onnxruntime，避免自编译 EP） |
| 索引输入 | **缩略图优先**，缺失时解原图并回写缩略图 |
| 缩略图 | 现有实现有 4 个缺陷（无失效/无 mtime 校验/键碰撞/解码慢），**需先改造**；批量补齐 9,472 张约 2 分钟 |
| 图像解码 | **libjpeg-turbo（DCT 降采样）**。纯 Go 全解码慢 24 倍，不可接受 |
| 向量检索 | SQLite 存 BLOB + **内存暴力余弦**。14k 条仅 28 MB，无需 HNSW/FAISS |
| 全量索引耗时 | **14k 张约 27 分钟**（GPU 推理 111ms 是硬下限） |
| 量化 | **不做**。不牺牲检索精度换空间 |

一句话：**GPU 推理 111ms 是天花板，其余环节都必须压到可忽略，否则总耗时会被放大。**

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

### 2.1 推理（ViT-B/16，224×224）

| 后端 | batch | ms/img | img/s |
|---|---|---|---|
| ORT CPU EP, 4 线程 | 1 | 729 | 1.37 |
| ORT CPU EP, 4 线程 | 8 | 776 | 1.29 |
| OpenVINO CPU | 1 | 763 | 1.31 |
| **OpenVINO GPU FP16** | **1** | **111** | **9.00** |
| OpenVINO GPU FP16 | 8 | 119 | 8.42 |
| OpenVINO GPU FP16 | 16 | 106 | 9.41 |
| OpenVINO GPU FP16 | 32 | 110 | 9.10 |

关键观察：
- **batch=1 就基本饱和**（9.0 vs 9.4 img/s），不需要复杂批处理
- CPU 侧 2 进程 × 2 线程只有 1.44 img/s，**多进程无收益**
- GPU 首次加载 8.6 s，需要预热

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
| 320 缩略图 | 4.7 | 111 | **116 ms** | 8.6 img/s | **27 min** |
| 12 MP 原图（libjpeg draft） | 17 | 111 | 128 ms | 7.8 img/s | 30 min |
| 12 MP 原图（Go 全解码） | 409 | 111 | 520 ms | 1.9 img/s | 121 min |
| HEIC 12 MP | 676 | 111 | 787 ms | 1.3 img/s | 3 h |

> **基准口径说明**：上述端到端测试的预处理用的是「直接 resize 到 224×224」（拉伸）。
> 正确的 CLIP 预处理是「短边缩放到 224 + 中心裁剪 224×224」。两者耗时差异可忽略
> （多一次裁剪，几微秒），但**实现时必须用后者**，否则宽高比失真会影响检索质量。

### 2.4 加速路径探索结论

- ✅ **iGPU 可用**：需 `intel-opencl-icd` + `libze-intel-gpu1`，用户在 `render` 组
- ❌ **多线程无收益**：CPU 已饱和，加线程反而更慢（解码线程与推理抢同一颗 die 的功耗/带宽）
- ❌ **量化不考虑**：用户明确要求保精度
- ❌ **降级到 MobileCLIP2 + 翻译**：不需要了，GPU 已够快

---

## 3. 关键决策（ADR）

### D1 模型：CN-CLIP ViT-B/16，512 维

**决策**：使用 `OFA-Sys/chinese-clip-vit-base-patch16`。

**理由**：
- 中文原生，文本塔是 RoBERTa-wwm，不需要"英文模型 + 翻译"的二次误差
- 17.6 GFLOPs，iGPU 上 9 img/s，14k 张 27 分钟
- 512 维向量，14k 条只占 28 MB

**否决项**：
- ViT-L/14（768 维，80 GFLOPs）：质量更好，但 4.5× FLOPs 推算约 500 ms/img，14k 需 ~2 小时，超出"一万张不能几个小时"的约束。**未实测**（模型下载受阻，见风险 R5）
- MobileCLIP2-S0：1.5 GFLOPs 很快，但文本塔是英文，必须翻译，引入误差且违背"中文原生"要求

**模型文件用 git-lfs 管理**，不入 Docker 镜像。

### D2 推理后端：OpenVINO C API（不引入 onnxruntime）

**决策**：Go 通过 CGO 直接调用 OpenVINO C API，用其 ONNX frontend 直接加载 `.onnx`。

**理由**：
- OpenVINO 官方 pip 包里已确认同时提供 `libopenvino_c.so`、`libopenvino_onnx_frontend.so`、`libopenvino_intel_gpu_plugin.so`（2025.4.1）
- 避免自编译 onnxruntime（`--use_openvino` 需要 cmake 全量构建，30+ 分钟，镜像巨大）
- 单一原生依赖，CPU/GPU 都走同一套 API，`device` 只需换字符串
- OpenVINO 官方提供 Ubuntu 24.04 apt 源与 `openvino/ubuntu24_runtime` 镜像

**否决项**：
- onnxruntime + OpenVINO EP：官方预编译包不含该 EP，必须自编译
- Python 子进程 sidecar：引入 Python 运行时，破坏单二进制交付
- onnxruntime CPU EP：763 ms，14k 需 3 小时，不可接受

**降级策略**：`device` 按 `GPU → CPU` 探测，GPU 不可用时降级并记录日志，功能不中断（只是慢 6.9 倍）。

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

### D6 文本编码在 CPU 上实时执行

查询时用 OpenVINO CPU 跑文本塔（RoBERTa-wwm-base，输入 ~32 token），预计 10~30 ms。加一层 LRU 缓存（容量 256）避免重复编码同一查询。

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
│  │  ThumbnailService 缩略图（含 mtime 校验，待修）    │    │
│  │  DedupService / HashService（P0）                 │    │
│  └───────────────────┬──────────────────────────────┘    │
│                      │                                   │
│  ┌───────────────────▼──────────────────────────────┐    │
│  │ internal/ai  (CGO)                                │    │
│  │  Encoder   图像/文本 → 向量                        │    │
│  │  Session   封装 OpenVINO compiled_model           │    │
│  │  Decoder   libjpeg-turbo / libheif                │    │
│  └───────────────────┬──────────────────────────────┘    │
└──────────────────────┼──────────────────────────────────┘
                       │
        ┌──────────────┴───────────────┐
        │                              │
┌───────▼────────┐          ┌──────────▼─────────┐
│ OpenVINO       │          │ SQLite (WAL)        │
│  GPU plugin    │          │  media_asset        │
│  (iGPU FP16)   │          │  media_embedding    │
│  CPU plugin    │          │  job / file_hash …  │
└────────────────┘          └─────────────────────┘
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

## 6. 缩略图子系统（需先改造）

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
3. **重建索引成本恒定**——换模型后不用再碰原图，恒 27 分钟

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
  → 短边等比缩放到 224（BICUBIC）
  → 中心裁剪 224×224
  → 像素值 /255
  → 逐通道减均值 [0.48145466, 0.4578275, 0.40821073]
  → 逐通道除标准差 [0.26862954, 0.26130258, 0.27577711]
  → 转 CHW，float32，连续内存
```

**注意**：是「短边缩放 + 中心裁剪」，不是直接拉伸到 224×224。拉伸会破坏宽高比，明显劣化检索质量。

缩略图场景下的具体路径：320×240 缩略图 → 短边 240 缩到 224 → 得 298×224 → 中心裁剪 224×224（横向裁掉约 25%）。可接受，因为缩略图本身是 `Fit` 等比缩放的，主体信息在中心。

### 7.3 并发模型

**关键：不要盲目加线程。** 实测 2 个解码线程反而从 8.96 掉到 4.46 img/s（CPU 与 iGPU 共享功耗预算，且推理本身已用满核心）。

推荐配置：

| 阶段 | goroutine 数 | 说明 |
|---|---|---|
| walker | 1 | 只做 stat，IO 轻 |
| decoder | 1（可配 2） | 4.7~17 ms，远小于推理 111 ms，无需并行 |
| encoder | **1** | GPU 串行，batch=1 已饱和 |
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

Web 请求与索引共存时，GPU 推理会抢占；实测单张 111 ms 的延迟抖动对检索（同样要过 GPU）影响可感知。**建议索引任务在低峰期执行**，前端提供"夜间索引"选项。

---

## 8. 检索流程

### 8.1 文搜图

```
query "海边日落"
  → 分词（HuggingFace tokenizer，Go 侧实现 BPE）
  → 文本塔推理（CPU，10~30 ms）
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
- 进程启动时惰性加载（首次检索触发），避免拖慢启动

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
      { "path": "照片/2024/IMG_1234.jpg", "score": 0.312, "thumb_url": "/api/files/thumbnail?path=..." }
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
      "backend":"openvino-2025.4.1",
      "device":"GPU", "gpu_name":"Intel UHD Graphics",
      "image_model_loaded":true, "text_model_loaded":true,
      "warmup_ms":8612, "dim":512 } }
```

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
    "search": {
      "default_limit": 50,
      "text_min_score": 0.18,
      "image_min_score": 0.75
    },
    "extensions": [".jpg", ".jpeg", ".png", ".webp", ".bmp", ".gif", ".heic", ".heif"]
  }
}
```

- `device` 支持 `GPU` / `CPU` / `AUTO`，探测失败自动降级到 `CPU` 并写日志
- `thumbnail.key` 仅 `sha1` 一个取值（保留字段是为了后续可扩展，旧值 `path` 不再支持）
- `thumbnail.max_size` / `jpeg_quality` **不要随意调**：索引侧假设短边 ≥ 224

---

## 11. 构建与部署

### 11.1 镜像改造（重要）

当前 `server/Dockerfile.server` 是 **alpine + `CGO_ENABLED=0`**，无法承载 OpenVINO / libjpeg-turbo / libheif（均为 glibc 构建）。必须改为 Debian 基底：

```dockerfile
# ---- 构建阶段 ----
FROM golang:1.22-bookworm AS builder
WORKDIR /build
ENV GOPROXY=https://goproxy.cn,direct
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ .
RUN CGO_ENABLED=1 go build -tags openvino -ldflags="-s -w" -o mini-nas-server ./cmd/server

# ---- 运行阶段 ----
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates tzdata ffmpeg \
      libjpeg-turbo-progs libjpeg62-turbo \
      libheif1 \
      libopenvino2025 \
      intel-opencl-icd libze-intel-gpu1 \
    && rm -rf /var/lib/apt/lists/*
# OpenVINO 运行库若 apt 源未提供，改为 COPY --from=openvino/ubuntu24_runtime:2025.4.1 /opt/intel/openvino/runtime /opt/intel/openvino/runtime
ENV LD_LIBRARY_PATH=/opt/intel/openvino/runtime/lib/intel64
WORKDIR /app
COPY --from=builder /build/mini-nas-server .
ENTRYPOINT ["./mini-nas-server", "-config", "/data/config.json", "-data", "/data"]
```

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
      - LD_LIBRARY_PATH=/opt/intel/openvino/runtime/lib/intel64
```

**宿主前置条件**（目标机已完成）：

```bash
sudo apt-get install -y intel-opencl-icd libze-intel-gpu1
sudo usermod -aG render $USER     # 生效需重新登录
ls /dev/dri/                       # 应有 card1 与 renderD128
```

### 11.3 模型入库（git-lfs）

```
models/cn-clip-vit-b16/
  image_encoder.onnx     # ~350 MB
  text_encoder.onnx      # ~400 MB
  vocab.txt
  merges.txt
  tokenizer.json
  meta.json              # {"model_id","dim":512,"image_size":224,"mean":[...],"std":[...]}
```

`.gitattributes`：

```
*.onnx filter=lfs diff=lfs merge=lfs -text
```

模型导出用 `scripts/export_clip_onnx.py`（已写好，待实跑验证）。

### 11.4 本地开发（Windows，无 gcc）

```bash
go build ./...                              # 默认 !cgo，AI 返回 503，其余功能正常
go build -tags openvino ./cmd/server        # 需 gcc + OpenVINO，仅 Linux
```

---

## 12. 实施计划

| 阶段 | 内容 | 交付物 | 验收标准 |
|---|---|---|---|
| **P0** | 去重（MD5 / 扫描 / 合并） | 已完成 | 本地 9 个场景全部通过 |
| **P1** | 模型导出与入库 | `models/` + lfs | ONNX 在 Python 下输出与 PyTorch 一致（余弦 > 0.999） |
| **P2** | `internal/ai` 推理内核 | CGO 封装 + 自检命令 | `server -selftest` 打印 device / warmup / 单图耗时 |
| **P2.5** | **缩略图子系统改造**（§6） | `ThumbnailService` 重写 + backfill 任务 | 覆盖率 100%；同路径覆盖后不再返回旧图；9,472 张补齐 < 5 分钟 |
| **P3** | 索引流水线 | `IndexService` + `/api/index/*` | 14k 张全量 < 40 分钟，可取消、可续跑 |
| **P4** | 检索 API | `/api/search/*` | 中文查询 top-10 人工相关度 ≥ 7/10 |
| **P5** | 前端 | Web 搜索页 + Android 入口 | 端到端可用 |
| **P6** | 校准与调优 | 分数阈值 + 并发参数 | 误检率可接受，GPU 利用率稳定 |

### P2.5 拆分（缩略图，必须先于索引）

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

1. `internal/ai/openvino.go` — CGO 绑定：`ov_core_create` / `ov_core_read_model` / `ov_core_compile_model` / `ov_compiled_model_create_infer_request` / `ov_infer_request_infer`
2. `internal/ai/encoder.go` — 图像预处理（短边缩放 + 中心裁剪 + 归一化 + CHW）与后处理（L2 归一化）
3. `internal/ai/decode_cgo.go` — libjpeg-turbo（`scale_denom`）+ libheif
4. `internal/ai/stub.go` — `!cgo` 空实现，返回 `ErrUnavailable`
5. `internal/ai/selftest.go` — 启动自检与预热
6. **`internal/ai/tokenizer.go` — BERT WordPiece 分词器（Go 实现）**

> **第 6 项容易被低估。** 导出脚本只产出 `vocab.txt`（一行一个 token，行号即 id），
> Go 侧需要自己实现：BasicTokenizer（含中文按字切分、小写化、标点切分）+ WordPiece
> 贪心最长匹配 + `[CLS]`/`[SEP]` 包装 + `attention_mask` 生成。
> 约 200~300 行，可参考 `sugarme/tokenizer` 的实现方式，但建议手写以精确对齐
> `BertTokenizer(do_lower_case=True)` 的行为。
> **验收**：同一批中文 query，Go 输出的 `input_ids` 与 Python
> `ChineseCLIPProcessor` 完全一致（逐 token 比对）。

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
| R1 | OpenVINO C API 的 Go 封装工作量被低估 | P2 延期 | 先做最小闭环（单图推理），跑通再扩；参考 `openvino/c` 头文件 |
| R2 | 容器内 GPU 直通失败 | 退回 CPU，慢 6.9× | 先裸机验证（已完成），再验证容器；`/api/index/health` 暴露 device |
| R3 | Debian 上 `libheif1` 编译/运行异常 | HEIC 索引失败 | 单张失败记 `error` 字段跳过，不中断任务；必要时换 `heif-convert` 子进程 |
| R4 | 缩略图子系统四个缺陷 T1~T4（§6.3） | 索引到过期内容 / 显示旧图 / 缓存泄漏 | 提到 **P2.5** 独立处理，先于索引流水线 |
| R5 | ViT-L/14 吞吐**未实测**（HF 下载受阻，`huggingface.co` 不可达、`hf-mirror.com` 慢到超时） | 无法确认是否值得换大模型 | 按 FLOPs 推算约 500 ms/img，已足以否决；若后续要重估，用 `HF_ENDPOINT=https://hf-mirror.com` 重试 |
| R6 | OpenVINO 首次加载 8.6 s | 首次检索卡顿 | 启动后异步预热，`/api/index/health` 暴露 `warmup_ms` |
| R7 | `MountService.AddDefaultMount` 是死代码 | 全新部署零挂载，首次上传必失败 | **P0 遗留 bug，需单独修**：在启动时若挂载表为空则创建默认挂载 |
| R8 | 索引期间 Web 卡顿 | 体验差 | `throttle_ms` + 夜间任务；检索与索引共用 GPU 需排队 |
| R9 | 纯 Go 解码路径（409 ms）未做并发优化 | `!cgo` 构建下索引极慢 | 仅用于开发，不做性能承诺；文档标注 |
| R10 | Go 侧 BERT 分词器与 Python 不一致 | 查询向量偏移，检索质量下降 | 用固定 30 条中英混合 query 做 token 级对拍，纳入 P2 验收 |
| R11 | 导出脚本未产出 `tokenizer.json` | 需手写分词器 | 已写入 P2 第 6 项；如需省事可改脚本用 `tokenizer.save_pretrained()` 额外导出 |
| R12 | 缩略图缓存键切换为哈希是破坏性变更 | 现有 4,508 个有效缩略图需重建 | 重建仅需 ~2 分钟；可在 backfill 任务里一次性完成，用户无感 |

---

## 附录 A：基准复现方法

所有数据来自目标机 `ssh.zhangjh.cn`，脚本已入库到 `scripts/bench/`（含 README 与陷阱说明）。

```bash
# 环境（一次性）
sudo apt-get install -y intel-opencl-icd libze-intel-gpu1
sudo usermod -aG render $USER
python3 -m venv /tmp/bench && /tmp/bench/bin/pip install \
  onnxruntime-openvino numpy pillow pillow-heif

# 推理基准（必须用 sg render 切换组，否则 GPU 不可见）
sg render -c "/tmp/bench/bin/python scripts/bench/bench_gpu_sweep.py"

# 端到端基准
sg render -c "/tmp/bench/bin/python scripts/bench/pipe2.py"

# 缩略图尺寸分布
/tmp/bench/bin/python scripts/bench/sz.py
```

**注意**：GPU 测试必须经 `sg render -c`，否则 OpenVINO 报 `Device GPU is not available` 并静默回退 CPU（表现为 722 ms，极易误判）。

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
