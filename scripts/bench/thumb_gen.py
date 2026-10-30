"""批量生成缩略图的吞吐测试。

对比三条路径生成 320 缩略图的成本：
  1. ffmpeg 单进程串行
  2. ffmpeg 多进程并行
  3. 纯 Go（imaging.Fit + Lanczos，见设计文档 2.2 节：530 ms/张）

用于评估「预生成补齐缩略图」是否划算。
"""

import os
import subprocess
import sys
import time
from concurrent.futures import ThreadPoolExecutor

SRC_DIR = "/mnt/nas/照片"
OUT_DIR = "/tmp/bench/thumbs"
N = int(os.environ.get("N", "40"))


def collect(n: int):
    out = []
    for root, _, files in os.walk(SRC_DIR):
        for f in files:
            if f.lower().endswith((".jpg", ".jpeg")):
                out.append(os.path.join(root, f))
                if len(out) >= n:
                    return out
    return out


def one(src: str, dst: str):
    # 与 Go 的 imaging.Fit(320,320,Lanczos) 等价：等比缩放到 320 框内
    vf = "scale=320:320:force_original_aspect_ratio=decrease:flags=lanczos"
    cmd = [
        "ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
        "-i", src,
        "-vf", vf,
        "-q:v", "3",
        dst,
    ]
    subprocess.run(cmd, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def bench(files, workers: int):
    os.makedirs(OUT_DIR, exist_ok=True)
    t0 = time.time()
    if workers == 1:
        for i, f in enumerate(files):
            one(f, os.path.join(OUT_DIR, f"t{i:04d}.jpg"))
    else:
        with ThreadPoolExecutor(max_workers=workers) as ex:
            list(ex.map(lambda p: one(p[1], os.path.join(OUT_DIR, f"t{p[0]:04d}.jpg")),
                        list(enumerate(files))))
    dt = time.time() - t0
    return dt


if __name__ == "__main__":
    files = collect(N)
    if not files:
        sys.exit("no source images")
    print(f"src={len(files)} files, sample={files[0]}")

    # 预热一次，排除首次进程启动开销
    os.makedirs(OUT_DIR, exist_ok=True)
    one(files[0], os.path.join(OUT_DIR, "warm.jpg"))

    sizes = []
    for w in (1, 2, 4):
        dt = bench(files, w)
        per = dt / len(files)
        print(f"ffmpeg workers={w}: {dt:6.2f}s total  {per * 1000:7.1f} ms/img  "
              f"{len(files) / dt:6.2f} img/s", flush=True)

    for f in os.listdir(OUT_DIR):
        if f.endswith(".jpg"):
            sizes.append(os.path.getsize(os.path.join(OUT_DIR, f)))
    if sizes:
        print(f"output size: avg {sum(sizes) // len(sizes)} B  min {min(sizes)}  max {max(sizes)}")
