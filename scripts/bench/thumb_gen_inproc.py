"""进程内生成 320 缩略图的成本（libjpeg 路径的代理测量）。

对比：
  A. 全分辨率解码 + LANCZOS 缩放（等价于现有 Go imaging.Fit）
  B. DCT 降采样解码（draft）+ 缩放（等价于 libjpeg-turbo scale_denom）

单线程与多线程都测。多线程用于评估批量补齐的摊薄成本。
"""

import io
import os
import time
from concurrent.futures import ThreadPoolExecutor

from PIL import Image

SRC_DIR = "/mnt/nas/照片"
OUT_DIR = "/tmp/bench/thumbs2"
N = int(os.environ.get("N", "40"))
BOX = 320


def collect(n: int):
    out = []
    for root, _, files in os.walk(SRC_DIR):
        for f in files:
            if f.lower().endswith((".jpg", ".jpeg")):
                out.append(os.path.join(root, f))
                if len(out) >= n:
                    return out
    return out


def gen_full(src: str) -> bytes:
    """A：全解码 + Lanczos（现有 Go 实现的做法）。"""
    with Image.open(src) as im:
        im = im.convert("RGB")
        im.thumbnail((BOX, BOX), Image.LANCZOS)
        buf = io.BytesIO()
        im.save(buf, "JPEG", quality=80)
        return buf.getvalue()


def gen_draft(src: str) -> bytes:
    """B：DCT 降采样解码 + Lanczos（libjpeg-turbo scale_denom 的做法）。"""
    with Image.open(src) as im:
        im.draft("RGB", (BOX, BOX))  # 关键：只解到够用的 DCT 层级
        im = im.convert("RGB")
        im.thumbnail((BOX, BOX), Image.LANCZOS)
        buf = io.BytesIO()
        im.save(buf, "JPEG", quality=80)
        return buf.getvalue()


def bench(fn, files, workers: int, label: str):
    os.makedirs(OUT_DIR, exist_ok=True)

    def task(idx_path):
        idx, p = idx_path
        data = fn(p)
        with open(os.path.join(OUT_DIR, f"{label}_{idx:04d}.jpg"), "wb") as f:
            f.write(data)
        return len(data)

    items = list(enumerate(files))
    t0 = time.time()
    if workers == 1:
        sizes = [task(it) for it in items]
    else:
        with ThreadPoolExecutor(max_workers=workers) as ex:
            sizes = list(ex.map(task, items))
    dt = time.time() - t0
    per = dt / len(files)
    print(f"{label:10s} workers={workers}: {dt:6.2f}s  {per * 1000:7.1f} ms/img  "
          f"{len(files) / dt:6.2f} img/s  avg {sum(sizes) // len(sizes)} B", flush=True)


if __name__ == "__main__":
    files = collect(N)
    print(f"src={len(files)}")
    gen_draft(files[0])  # 预热
    gen_full(files[0])

    bench(gen_full, files, 1, "full")
    bench(gen_draft, files, 1, "draft")
    bench(gen_draft, files, 2, "draft")
    bench(gen_draft, files, 4, "draft")
