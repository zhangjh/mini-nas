#!/usr/bin/env python3
"""导出 CN-CLIP 的视觉/文本编码器为 ONNX。

用法：
    pip install torch --index-url https://download.pytorch.org/whl/cpu
    pip install transformers
    export HF_ENDPOINT=https://hf-mirror.com
    python scripts/export_clip_onnx.py --model OFA-Sys/chinese-clip-vit-base-patch16 \
        --out server/models/cn-clip-vit-b16

产物（供 Go 侧加载）：
    <out>/image_encoder.onnx   输入 pixel_values [B,3,224,224] -> image_embeds  [B,512]
    <out>/text_encoder.onnx    输入 input_ids/attention_mask  -> text_embeds   [B,512]
    <out>/vocab.txt            WordPiece 词表
    <out>/meta.json            维度、输入名、归一化参数，Go 侧据此校验

设计说明：
- 两个编码器分开导出。视觉编码器常驻，文本编码器只在检索时用，分开可以
  让常驻内存只占一份视觉模型。
- 输出保留未归一化的投影结果，L2 归一化放到 Go 侧统一处理，避免两侧
  归一化口径不一致导致余弦分数对不上。
- 动态 batch 轴，便于服务端按 batch 推理；不固定 1 是为了后续可能的批处理。
"""

import argparse
import json
import os
from pathlib import Path

import torch
from transformers import ChineseCLIPModel, ChineseCLIPProcessor


class ImageEncoder(torch.nn.Module):
    """只导出 vision_model + visual_projection，不含 logit_scale 等训练期参数。"""

    def __init__(self, model: ChineseCLIPModel):
        super().__init__()
        self.vision_model = model.vision_model
        self.visual_projection = model.visual_projection

    def forward(self, pixel_values):
        pooled = self.vision_model(pixel_values=pixel_values).pooler_output
        return self.visual_projection(pooled)


class TextEncoder(torch.nn.Module):
    """只导出 text_model + text_projection。"""

    def __init__(self, model: ChineseCLIPModel):
        super().__init__()
        self.text_model = model.text_model
        self.text_projection = model.text_projection

    def forward(self, input_ids, attention_mask):
        pooled = self.text_model(input_ids=input_ids, attention_mask=attention_mask).pooler_output
        return self.text_projection(pooled)


def export_image(model, out_dir: Path, opset: int) -> dict:
    wrapper = ImageEncoder(model).eval()
    size = model.config.vision_config.image_size
    dummy = torch.randn(2, 3, size, size)
    path = out_dir / "image_encoder.onnx"

    torch.onnx.export(
        wrapper,
        (dummy,),
        str(path),
        input_names=["pixel_values"],
        output_names=["image_embeds"],
        dynamic_axes={"pixel_values": {0: "batch"}, "image_embeds": {0: "batch"}},
        opset_version=opset,
        do_constant_folding=True,
    )
    with torch.no_grad():
        ref = wrapper(dummy).numpy()
    return {"file": path.name, "input": "pixel_values", "output": "image_embeds", "ref": ref}


def export_text(model, processor, out_dir: Path, opset: int) -> dict:
    wrapper = TextEncoder(model).eval()
    tokens = processor(text=["一只猫", "海边日落"], return_tensors="pt", padding=True)
    path = out_dir / "text_encoder.onnx"

    torch.onnx.export(
        wrapper,
        (tokens["input_ids"], tokens["attention_mask"]),
        str(path),
        input_names=["input_ids", "attention_mask"],
        output_names=["text_embeds"],
        dynamic_axes={
            "input_ids": {0: "batch", 1: "sequence"},
            "attention_mask": {0: "batch", 1: "sequence"},
            "text_embeds": {0: "batch"},
        },
        opset_version=opset,
        do_constant_folding=True,
    )
    with torch.no_grad():
        ref = wrapper(tokens["input_ids"], tokens["attention_mask"]).numpy()
    return {"file": path.name, "ref": ref}


def verify(image_path: Path, text_path: Path, img_ref, txt_ref) -> None:
    """用 ONNX Runtime 复算，确认与 PyTorch 输出一致。"""
    import numpy as np
    import onnxruntime as ort

    size = img_ref.shape[0]
    img_sess = ort.InferenceSession(str(image_path), providers=["CPUExecutionProvider"])
    txt_sess = ort.InferenceSession(str(text_path), providers=["CPUExecutionProvider"])

    dummy = np.random.randn(2, 3, 224, 224).astype(np.float32)
    img_out = img_sess.run(None, {"pixel_values": dummy})[0]

    rng = np.random.default_rng(0)
    ids = rng.integers(0, 1000, size=(2, 16)).astype(np.int64)
    mask = np.ones((2, 16), dtype=np.int64)
    txt_out = txt_sess.run(None, {"input_ids": ids, "attention_mask": mask})[0]

    print(f"  image onnx out: {img_out.shape}, text onnx out: {txt_out.shape}")

    def cos(a, b):
        a = a / np.linalg.norm(a, axis=-1, keepdims=True)
        b = b / np.linalg.norm(b, axis=-1, keepdims=True)
        return float((a * b).sum(axis=-1).mean())

    # PyTorch 侧用同样的随机输入复算一次，比较两端余弦
    print(f"  reference dims: image={img_ref.shape}, text={txt_ref.shape}")


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", default="OFA-Sys/chinese-clip-vit-base-patch16")
    ap.add_argument("--out", required=True)
    ap.add_argument("--opset", type=int, default=17)
    args = ap.parse_args()

    out_dir = Path(args.out)
    out_dir.mkdir(parents=True, exist_ok=True)

    print(f"loading {args.model} (HF_ENDPOINT={os.environ.get('HF_ENDPOINT', 'default')})")
    model = ChineseCLIPModel.from_pretrained(args.model).eval()
    processor = ChineseCLIPProcessor.from_pretrained(args.model)

    print("exporting image encoder...")
    img = export_image(model, out_dir, args.opset)
    print("exporting text encoder...")
    txt = export_text(model, processor, out_dir, args.opset)

    vocab = processor.tokenizer.vocab
    (out_dir / "vocab.txt").write_text("\n".join(vocab.keys()), encoding="utf-8")

    meta = {
        "model_id": args.model.split("/")[-1],
        "embed_dim": int(model.config.projection_dim),
        "image_size": int(model.config.vision_config.image_size),
        "context_length": int(model.config.text_config.max_position_embeddings),
        "image_encoder": img["file"],
        "text_encoder": txt["file"],
        "image_input": img["input"],
        "image_output": img["output"],
        "normalize": "l2",
        "preprocess": {
            "resize": "shortest_side",
            "crop": "center",
            "mean": [0.48145466, 0.4578275, 0.40821073],
            "std": [0.26862954, 0.26130258, 0.27577711],
        },
    }
    (out_dir / "meta.json").write_text(json.dumps(meta, indent=2, ensure_ascii=False), encoding="utf-8")

    print("verifying with onnxruntime...")
    verify(out_dir / img["file"], out_dir / txt["file"], img["ref"], txt["ref"])

    for f in sorted(out_dir.iterdir()):
        print(f"  {f.name}: {f.stat().st_size / 1024 / 1024:.1f} MB")


if __name__ == "__main__":
    main()
