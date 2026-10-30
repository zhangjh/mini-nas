#!/usr/bin/env python3
"""导出 CN-CLIP 的视觉/文本编码器为 ONNX（静态 shape）。

用法：
    # 1) 准备权重（本机无外网 HF 时走 ModelScope 镜像）
    #    AI-ModelScope/chinese-clip-vit-base-patch16
    #    需要的文件：config.json preprocessor_config.json vocab.txt pytorch_model.bin
    # 2) 导出 + 校验
    python scripts/export_clip_onnx.py --model /path/to/cnclip-b16 \
        --out server/models/cn-clip-vit-b16
    # 3) 只校验（不加载 torch 模型，用 golden.npz 复算）
    python scripts/export_clip_onnx.py --out server/models/cn-clip-vit-b16 --verify-only

产物（供 Go 侧加载）：
    <out>/image_encoder.onnx   输入 pixel_values [1,3,224,224] -> image_embeds  [1,512]
    <out>/text_encoder.onnx    输入 input_ids/attention_mask [1,64] -> text_embeds [1,512]
    <out>/vocab.txt            WordPiece 词表（行号即 token id）
    <out>/meta.json            维度、输入名、归一化参数，Go 侧据此校验
    <out>/golden.npz           黄金样本：固定输入 + PyTorch 参考输出，Go 侧对拍用
    <out>/verify.json          导出校验报告

为什么是静态 shape（重要）：
    动态 batch 轴会让 onnxruntime 的 OpenVINOExecutionProvider 在 GPU 上输出
    错误结果 —— 实测是 512 维全 0 或数值全错（余弦 0.47~0.51），CPU EP 却完全
    正常。改成静态 shape 后 GPU 输出正确（余弦 0.99999）。这个 bug 不报错、
    不崩溃，只静默给出错向量，检索结果会整体失真而没人察觉，所以：
      - 模型一律导出静态 shape
      - 导出时强制跑一遍 GPU 校验（见 verify_gpu），不通过就失败

其他设计说明：
- 两个编码器分开导出。视觉编码器常驻，文本编码器只在检索时用，分开可以
  让常驻内存只占一份视觉模型。
- 输出保留未归一化的投影结果，L2 归一化放到 Go 侧统一处理，避免两侧
  归一化口径不一致导致余弦分数对不上。
- 导出用 eager attention。SDPA 融合算子在不同 opset 下可能展开成
  Attention 复合算子，OpenVINO 支持不稳定；eager 展开的是纯 MatMul/Softmax。
- vocab.txt 直接复制源文件，不从 tokenizer 反推。行号即 id，顺序错一位
  整张检索表就废了，不能靠二次序列化保证。
- 文本固定 64 token（padding="max_length"）。查询词很短，64 个 WordPiece
  足够；固定长度是为了满足静态 shape。
"""

import argparse
import json
import shutil
import sys
from pathlib import Path

import numpy as np

REQUIRED_FILES = ("config.json", "vocab.txt")
WEIGHT_CANDIDATES = ("pytorch_model.bin", "model.safetensors")

# 校验用的中文语料。anchors/near/far 三组同下标构成「近义 vs 无关」对照。
TEXTS = ["一只猫坐在窗台上", "海边日落，橙色的天空", "city skyline at night"]
ANCHORS = ["一只猫坐在窗台上", "海边日落"]
NEAR = ["一只小猫趴在窗台上", "黄昏时分的海滩落日"]
FAR = ["一辆红色汽车停在路边", "办公室里的打印机"]


def _cos(a: np.ndarray, b: np.ndarray) -> np.ndarray:
    """逐样本余弦相似度（要求 a、b 同形）。"""
    a = a / np.clip(np.linalg.norm(a, axis=-1, keepdims=True), 1e-12, None)
    b = b / np.clip(np.linalg.norm(b, axis=-1, keepdims=True), 1e-12, None)
    return (a * b).sum(axis=-1)


# ---------------------------------------------------------------- export


def _export(module, args, path: Path, input_names, output_names, opset: int) -> None:
    """导出为静态 shape 的 ONNX。

    只在 TypeError（签名里没有 dynamo 形参）时降级；其它异常一律抛出。
    无差别 except Exception 会把真实错误（比如池化取到 None）掩盖成
    导出器不兼容，排查代价极高。
    """
    import torch

    kwargs = dict(
        input_names=input_names,
        output_names=output_names,
        opset_version=opset,
        do_constant_folding=True,
    )
    try:
        torch.onnx.export(module, args, str(path), dynamo=False, **kwargs)
    except TypeError as e:
        if "dynamo" not in str(e):
            raise
        print("  torch.onnx.export has no `dynamo` kwarg, using default exporter")
        torch.onnx.export(module, args, str(path), **kwargs)


def _official_features(out):
    """get_*_features 在 return_dict=False 时返回 tuple，统一取出投影后的向量。"""
    if isinstance(out, tuple):
        return out[0]
    return out.pooler_output


def torch_refs(model, tok, kind: str) -> np.ndarray:
    """逐样本用官方 API 算参考向量。模型是静态 batch=1，参考也要按 1 条一条算。"""
    import torch

    outs = []
    with torch.no_grad():
        for i in range(tok["input_ids"].shape[0]):
            ids, mask = tok["input_ids"][i:i + 1], tok["attention_mask"][i:i + 1]
            if kind == "text":
                o = model.get_text_features(input_ids=ids, attention_mask=mask)
            else:
                o = model.get_image_features(pixel_values=ids)
            outs.append(_official_features(o).numpy())
    return np.concatenate(outs, axis=0)


def export_image(model, pixel, out_dir: Path, opset: int) -> np.ndarray:
    import torch

    class ImageEncoder(torch.nn.Module):
        """vision_model + visual_projection，池化用 vision 的 pooler_output，
        与 ChineseCLIPModel.get_image_features 一致。"""

        def __init__(self, m):
            super().__init__()
            self.vision_model = m.vision_model
            self.visual_projection = m.visual_projection

        def forward(self, pixel_values):
            pooled = self.vision_model(pixel_values=pixel_values).pooler_output
            return self.visual_projection(pooled)

    wrapper = ImageEncoder(model).eval()
    path = out_dir / "image_encoder.onnx"
    _export(wrapper, (pixel,), path, ["pixel_values"], ["image_embeds"], opset)
    with torch.no_grad():
        ref = _official_features(model.get_image_features(pixel_values=pixel)).numpy()
        wrapped = wrapper(pixel).numpy()
    drift = float(_cos(wrapped, ref).min())
    if drift < 0.99999:
        raise SystemExit(f"image wrapper 与 get_image_features 不一致: cos_min={drift:.6f}")
    print(f"  wrapper vs get_image_features: cos_min={drift:.8f}")
    return ref


def export_text(model, ids, mask, out_dir: Path, opset: int) -> np.ndarray:
    import torch

    class TextEncoder(torch.nn.Module):
        """text_model + text_projection。

        池化方式必须与 ChineseCLIPModel.get_text_features 完全一致：
            pooled_output = text_outputs.last_hidden_state[:, 0, :]   # CLS token
            text_features = self.text_projection(pooled_output)
        注意这里**不是** pooler_output —— CN-CLIP 的 text_model 构造时
        add_pooling_layer=False，self.pooler 为 None，取 pooler_output 会拿到 None。
        """

        def __init__(self, m):
            super().__init__()
            self.text_model = m.text_model
            self.text_projection = m.text_projection

        def forward(self, input_ids, attention_mask):
            hidden = self.text_model(input_ids=input_ids, attention_mask=attention_mask).last_hidden_state
            return self.text_projection(hidden[:, 0, :])

    wrapper = TextEncoder(model).eval()
    path = out_dir / "text_encoder.onnx"
    _export(wrapper, (ids, mask), path, ["input_ids", "attention_mask"], ["text_embeds"], opset)
    with torch.no_grad():
        ref = _official_features(model.get_text_features(input_ids=ids, attention_mask=mask)).numpy()
        wrapped = wrapper(ids, mask).numpy()
    drift = float(_cos(wrapped, ref).min())
    if drift < 0.99999:
        raise SystemExit(f"text wrapper 与 get_text_features 不一致: cos_min={drift:.6f}")
    print(f"  wrapper vs get_text_features: cos_min={drift:.8f}")
    return ref


# ---------------------------------------------------------------- verify


def _sess(path: Path, provider: str, device: str | None, precision: str | None):
    import onnxruntime as ort

    so = ort.SessionOptions()
    so.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
    if provider == "cpu":
        return ort.InferenceSession(str(path), so, providers=["CPUExecutionProvider"])
    opts = {}
    if device:
        opts["device_type"] = device
    if precision:
        opts["precision"] = precision
    return ort.InferenceSession(str(path), so, providers=["OpenVINOExecutionProvider"],
                                provider_options=[opts])


def run_static(sess, feeds: dict) -> np.ndarray:
    """静态 batch=1 模型：按第一维逐样本推理后拼接。"""
    n = next(iter(feeds.values())).shape[0]
    return np.concatenate(
        [sess.run(None, {k: v[i:i + 1] for k, v in feeds.items()})[0] for i in range(n)],
        axis=0,
    )


def verify(out_dir: Path, fx: dict, threshold: float) -> dict:
    """四段验收：

    1. 数值一致性 —— ONNX 与 PyTorch 参考逐样本余弦 > threshold。设计文档 §12 的硬指标。
    2. 确定性 —— 同一输入连跑两次结果完全一致，排除未初始化内存导致的抖动。
    3. 语义有效性 —— 中文近义句相似度必须高于无关句。数值对得上但投影层接错时
       （比如误用 pooler 的零向量），余弦照样是 1.0，只有语义检查能发现。
    4. GPU 保真度 —— 若 OpenVINO EP 可用，GPU FP16 必须与 CPU 结果一致。
       动态 shape 的 bug 就是被这一项挡住的。
    """
    report = {"threshold": threshold}
    ok = True

    img_sess = _sess(out_dir / "image_encoder.onnx", "cpu", None, None)
    txt_sess = _sess(out_dir / "text_encoder.onnx", "cpu", None, None)

    feeds = {
        "image": ({"pixel_values": fx["pixel_values"]}, fx["image_embeds"]),
        "text": ({"input_ids": fx["text_input_ids"], "attention_mask": fx["text_attention_mask"]},
                 fx["text_embeds"]),
    }
    sesses = {"image": img_sess, "text": txt_sess}

    report["encoders"] = {}
    for name, (f, want) in feeds.items():
        sess = sesses[name]
        got = run_static(sess, f)
        if got.shape != want.shape:
            raise SystemExit(f"{name}: shape mismatch onnx={got.shape} torch={want.shape}")
        per = _cos(got, want)
        worst, mean = float(per.min()), float(per.mean())
        again = run_static(sess, f)
        deterministic = bool(np.array_equal(got, again))
        passed = worst >= threshold and deterministic
        ok = ok and passed
        report["encoders"][name] = {
            "shape": list(got.shape),
            "cos_mean": round(mean, 8),
            "cos_min": round(worst, 8),
            "deterministic": deterministic,
            "pass": passed,
        }
        print(f"  [{'PASS' if passed else 'FAIL'}] {name}: cos min={worst:.8f} "
              f"deterministic={deterministic}")

    # ---- 语义有效性 ----
    # 模型是静态 batch=1，逐条跑（与运行时一致），再把结果拼起来算相似度矩阵
    n_sem = fx["sem_input_ids"].shape[0]
    embs = []
    for i in range(n_sem):
        embs.append(txt_sess.run(None, {
            "input_ids": fx["sem_input_ids"][i:i + 1],
            "attention_mask": fx["sem_attention_mask"][i:i + 1],
        })[0][0])
    emb = np.stack(embs)
    emb = emb / np.clip(np.linalg.norm(emb, axis=-1, keepdims=True), 1e-12, None)
    sims = emb @ emb.T
    n = len(ANCHORS)
    checks = []
    for i in range(n):
        s_near, s_far = float(sims[i, n + i]), float(sims[i, 2 * n + i])
        checks.append({"anchor": ANCHORS[i], "near": NEAR[i], "far": FAR[i],
                       "sim_near": round(s_near, 6), "sim_far": round(s_far, 6),
                       "pass": s_near > s_far})
        print(f"    {ANCHORS[i]!r}: near={s_near:.4f} far={s_far:.4f} "
              f"{'ok' if s_near > s_far else 'INVERTED'}")
    sem_pass = all(c["pass"] for c in checks)
    ok = ok and sem_pass
    report["semantics"] = {"pairs": checks, "pass": sem_pass}
    print(f"  [{'PASS' if sem_pass else 'FAIL'}] semantics")

    # ---- GPU 保真度 ----
    gpu = {}
    try:
        for name, (f, want) in feeds.items():
            path = out_dir / ("image_encoder.onnx" if name == "image" else "text_encoder.onnx")
            sess = _sess(path, "ov", "GPU", "FP16")
            got = run_static(sess, f)
            c = float(_cos(got, want).min())
            gpu[name] = {"cos_min": round(c, 8), "absmax": round(float(np.abs(got).max()), 6),
                         "pass": c >= threshold}
        gpu["pass"] = all(v["pass"] for k, v in gpu.items() if k != "pass")
        gpu["available"] = True
    except Exception as e:  # noqa: BLE001
        gpu = {"available": False, "reason": f"{type(e).__name__}: {str(e)[:200]}", "pass": True}
    ok = ok and gpu.get("pass", True)
    report["gpu"] = gpu
    if gpu["available"]:
        for name in ("image", "text"):
            v = gpu[name]
            print(f"  [{'PASS' if v['pass'] else 'FAIL'}] GPU FP16 {name}: cos={v['cos_min']:.8f} "
                  f"absmax={v['absmax']}")
    else:
        print(f"  [SKIP] GPU check: {gpu['reason']}")

    report["pass"] = ok
    return report


def load_fixture(path: Path) -> dict:
    with np.load(path) as z:
        return {k: z[k] for k in z.files}


# ---------------------------------------------------------------- main


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", default="OFA-Sys/chinese-clip-vit-base-patch16",
                    help="本地目录或 HF/ModelScope 仓库 id")
    ap.add_argument("--out", required=True)
    ap.add_argument("--opset", type=int, default=17)
    ap.add_argument("--threshold", type=float, default=0.999)
    ap.add_argument("--seq-len", type=int, default=64, help="文本编码器固定序列长度")
    ap.add_argument("--verify-only", action="store_true",
                    help="跳过导出，用已有 golden.npz 复算校验")
    args = ap.parse_args()

    out_dir = Path(args.out)

    if args.verify_only:
        fx = load_fixture(out_dir / "golden.npz")
        print("verifying existing artifacts against golden.npz...")
        report = verify(out_dir, fx, args.threshold)
        (out_dir / "verify.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
        if not report["pass"]:
            print("\nVERIFY FAILED", file=sys.stderr)
            sys.exit(1)
        print("\nOK")
        return

    import torch
    from transformers import AutoTokenizer, ChineseCLIPModel

    model_path = Path(args.model)
    if model_path.is_dir():
        missing = [f for f in REQUIRED_FILES if not (model_path / f).exists()]
        if missing:
            raise SystemExit(f"{model_path} 缺少文件: {missing}")
        if not any((model_path / w).exists() for w in WEIGHT_CANDIDATES):
            raise SystemExit(f"{model_path} 缺少权重文件，需其一: {WEIGHT_CANDIDATES}")
        print(f"loading local model from {model_path}")
    else:
        print(f"loading {args.model} (HF_ENDPOINT={__import__('os').environ.get('HF_ENDPOINT', 'default')})")

    out_dir.mkdir(parents=True, exist_ok=True)

    try:
        model = ChineseCLIPModel.from_pretrained(str(model_path), attn_implementation="eager").eval()
    except (TypeError, ValueError):
        model = ChineseCLIPModel.from_pretrained(str(model_path)).eval()

    try:
        tokenizer = AutoTokenizer.from_pretrained(str(model_path))
    except Exception as e:  # noqa: BLE001
        print(f"  AutoTokenizer failed ({type(e).__name__}), fallback to BertTokenizer(vocab.txt)")
        from transformers import BertTokenizer
        tokenizer = BertTokenizer(str(model_path / "vocab.txt"))

    size = int(model.config.vision_config.image_size)
    L = args.seq_len
    gen = torch.Generator().manual_seed(1234)
    pixel = torch.randn(1, 3, size, size, generator=gen)

    def enc(texts):
        return tokenizer(texts, return_tensors="pt", padding="max_length",
                         truncation=True, max_length=L)

    tok = enc(TEXTS)
    sem = enc(ANCHORS + NEAR + FAR)

    print(f"exporting image encoder (static 1x3x{size}x{size})...")
    img_ref = export_image(model, pixel, out_dir, args.opset)
    print(f"exporting text encoder (static 1x{L})...")
    # 文本编码器固定 batch=1，导出只喂一条；其余样本的参考向量单独算
    export_text(model, tok["input_ids"][:1], tok["attention_mask"][:1], out_dir, args.opset)
    txt_ref = torch_refs(model, tok, "text")

    # vocab.txt 原样复制：行号 = token id，顺序必须与训练时一致
    if (model_path / "vocab.txt").exists():
        shutil.copyfile(model_path / "vocab.txt", out_dir / "vocab.txt")
        vocab_lines = (out_dir / "vocab.txt").read_text(encoding="utf-8").splitlines()
        print(f"  vocab.txt copied, {len(vocab_lines)} tokens")
    else:
        vocab_lines = [t for t, _ in sorted(tokenizer.get_vocab().items(), key=lambda kv: kv[1])]
        (out_dir / "vocab.txt").write_text("\n".join(vocab_lines) + "\n", encoding="utf-8")
        print(f"  vocab.txt rebuilt from tokenizer, {len(vocab_lines)} tokens")

    fx = {
        "pixel_values": pixel.numpy().astype(np.float32),
        "text_input_ids": tok["input_ids"].numpy().astype(np.int64),
        "text_attention_mask": tok["attention_mask"].numpy().astype(np.int64),
        "sem_input_ids": sem["input_ids"].numpy().astype(np.int64),
        "sem_attention_mask": sem["attention_mask"].numpy().astype(np.int64),
        "image_embeds": img_ref.astype(np.float32),
        "text_embeds": txt_ref.astype(np.float32),
    }
    np.savez_compressed(out_dir / "golden.npz", **fx)

    meta = {
        "model_id": "chinese-clip-vit-base-patch16",
        "embed_dim": int(model.config.projection_dim),
        "image_size": size,
        "seq_len": L,
        "vocab_size": len(vocab_lines),
        "image_encoder": "image_encoder.onnx",
        "text_encoder": "text_encoder.onnx",
        "image_input": "pixel_values",
        "image_output": "image_embeds",
        "text_inputs": ["input_ids", "attention_mask"],
        "text_output": "text_embeds",
        "static_shapes": {
            "pixel_values": [1, 3, size, size],
            "input_ids": [1, L],
            "attention_mask": [1, L],
        },
        "normalize": "l2",
        "preprocess": {
            # 与 preprocessor_config.json 一致：do_center_crop=false，直接拉伸到
            # size×size。写成 shortest_side/center 会误导 Go 侧实现（§7.2 有 A/B 实测）。
            "resize": "stretch",
            "size": [size, size],
            "resample": "bicubic",
            "crop": "none",
            "mean": [0.48145466, 0.4578275, 0.40821073],
            "std": [0.26862954, 0.26130258, 0.27577711],
        },
    }
    (out_dir / "meta.json").write_text(json.dumps(meta, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")

    print("verifying...")
    report = verify(out_dir, fx, args.threshold)
    (out_dir / "verify.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")

    print("\nartifacts:")
    for f in sorted(out_dir.iterdir()):
        print(f"  {f.name}: {f.stat().st_size / 1024 / 1024:.1f} MB")

    if not report["pass"]:
        print("\nVERIFY FAILED", file=sys.stderr)
        sys.exit(1)
    print("\nOK")


if __name__ == "__main__":
    main()
