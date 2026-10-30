#!/usr/bin/env python3
"""生成图像预处理黄金样本，供 Go 侧对拍。

为什么需要：模型数值已经能用 golden.npz 验证，但「解码 → resize → 归一化 → CHW」
这段 Go 重写是最容易出偏差的地方，而且偏了不报错、只让检索质量悄悄下降
（用错插值方式、把 stretch 写成 center-crop、RGB/BGR 颠倒、忘了 /255、
mean/std 顺序错 —— 这些都只表现为分数略低）。

产物 preproc_golden.npz：
    source_png    uint8[]      合成源图（PNG 字节），Go 用 image/png 直接解码
    expected      f32[1,3,224,224]  官方 processor 算出的目标张量
    source_w/h    int          源图尺寸

为什么用合成图而不是真实照片：可复现、不依赖外部素材、且刻意做成非正方形
非整除尺寸（641x427），能暴露 resize/crop 的取整错误。

参考实现用 transformers 的 AutoImageProcessor，即随权重发布的
preprocessor_config.json（do_center_crop=false → 直接拉伸到 224x224）。
不要改成「短边缩放 + 中心裁剪」—— A/B 实测官方拉伸的图文区分度更高
（margin +0.0725 vs +0.0669），见 scripts/bench/ab_preproc.py。

用法：
    python scripts/make_preproc_golden.py --model /path/to/cnclip-b16 \
        --out server/models/cn-clip-vit-b16/preproc_golden.npz
"""

import argparse
import io
from pathlib import Path

import numpy as np
from PIL import Image, ImageDraw

W, H = 641, 427  # 非正方形、非 224 整除，专门用来暴露取整错误


def synth_image() -> Image.Image:
    """确定性合成图：渐变底 + 彩色块 + 圆 + 细斜线。

    同时覆盖平滑区域（检验插值质量）和高频边缘（检验是否真的重采样）。
    """
    x = np.linspace(0, 1, W, dtype=np.float32)[None, :]
    y = np.linspace(0, 1, H, dtype=np.float32)[:, None]
    r = (x * 255).astype(np.uint8)
    g = (y * 255).astype(np.uint8)
    b = ((1 - x) * (1 - y) * 255).astype(np.uint8)
    arr = np.stack([np.broadcast_to(r, (H, W)),
                    np.broadcast_to(g, (H, W)),
                    np.broadcast_to(b, (H, W))], axis=-1)

    img = Image.fromarray(arr, mode="RGB")
    d = ImageDraw.Draw(img)
    d.rectangle([40, 60, 240, 220], fill=(220, 40, 40))
    d.rectangle([300, 250, 560, 400], fill=(40, 200, 80))
    d.ellipse([420, 40, 600, 220], fill=(60, 80, 230))
    for i in range(0, W, 7):  # 细斜线：高频成分，插值方式不同结果差异明显
        d.line([(i, 0), (i + 60, H)], fill=(255, 255, 0), width=1)
    return img


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", required=True, help="含 preprocessor_config.json 的权重目录")
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    from transformers import AutoImageProcessor

    proc = AutoImageProcessor.from_pretrained(args.model)
    print(f"processor: {type(proc).__name__}")
    print(f"  size={proc.size}  resample={proc.resample}  do_center_crop={proc.do_center_crop}")

    img = synth_image()
    buf = io.BytesIO()
    img.save(buf, format="PNG")
    png_bytes = buf.getvalue()

    # 走官方 processor，保证与 preprocessor_config.json 完全一致
    px = proc(images=img, return_tensors="np")["pixel_values"]
    assert px.shape == (1, 3, 224, 224), px.shape
    px = px.astype(np.float32)

    # 自检：用官方配置声明的参数手算一遍，确认 processor 行为与声明一致
    manual = img.resize((224, 224), Image.BICUBIC) if not proc.do_center_crop else None
    if manual is not None:
        a = np.asarray(manual, dtype=np.float32) / 255.0
        a = (a - np.array(proc.image_mean, dtype=np.float32)) / np.array(proc.image_std, dtype=np.float32)
        a = a.transpose(2, 0, 1)[None]
        drift = float(np.abs(a - px).max())
        print(f"  manual vs processor max|diff| = {drift:.2e}  "
              f"{'OK' if drift < 1e-5 else 'MISMATCH'}")
        if drift >= 1e-5:
            raise SystemExit("processor 行为与 preprocessor_config 声明不一致，先查清楚再生成")

    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    np.savez_compressed(
        out,
        source_png=np.frombuffer(png_bytes, dtype=np.uint8),
        expected=px,
        source_w=np.int32(img.size[0]),
        source_h=np.int32(img.size[1]),
        mean=np.array(proc.image_mean, dtype=np.float32),
        std=np.array(proc.image_std, dtype=np.float32),
    )
    print(f"\nwrote {out} ({out.stat().st_size / 1024:.1f} KB)")
    print(f"  source {img.size[0]}x{img.size[1]} -> expected {px.shape} {px.dtype}")
    print(f"  value range [{px.min():.4f}, {px.max():.4f}]")


if __name__ == "__main__":
    main()
