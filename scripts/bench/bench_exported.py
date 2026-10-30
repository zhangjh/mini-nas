"""用真实导出的 ONNX 产物复测推理性能与数值保真度。

在目标机上运行（必须经 sg render 切换组，否则 GPU 不可见、静默回退 CPU）：

    sg render -c "/tmp/bench/bin/python scripts/bench/bench_exported.py \
        --model /tmp/bench/out/cn-clip-vit-b16"

检查三件事：
1. ORT CPU / ORT+OpenVINO GPU 的延迟与吞吐
2. GPU FP16 相对 golden.npz（PyTorch 参考）的余弦保真度
   —— FP16 若把嵌入压坏，检索精度会掉，这个必须量化而不是"应该没事"
3. 首次加载（含 OpenVINO 编译）耗时，决定常驻进程的启动预算
"""

import argparse
import time
from pathlib import Path

import numpy as np
import onnxruntime as ort


def build(model_path: Path, device: str | None, precision: str | None):
    so = ort.SessionOptions()
    so.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
    if device is None:
        return ort.InferenceSession(str(model_path), so, providers=["CPUExecutionProvider"])
    opts = {"device_type": device}
    if precision:
        opts["precision"] = precision
    return ort.InferenceSession(
        str(model_path), so,
        providers=["OpenVINOExecutionProvider"],
        provider_options=[opts],
    )


def cos(a: np.ndarray, b: np.ndarray) -> np.ndarray:
    a = a / np.linalg.norm(a, axis=-1, keepdims=True)
    b = b / np.linalg.norm(b, axis=-1, keepdims=True)
    return (a * b).sum(axis=-1)


def bench(sess, feeds: dict, label: str, n: int) -> tuple[float, np.ndarray]:
    sess.run(None, feeds)  # warmup
    t = time.time()
    for _ in range(n):
        out = sess.run(None, feeds)[0]
    dt = (time.time() - t) / n
    print(f"  {label}: {dt * 1000:7.1f} ms  ", flush=True)
    return dt, out


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", required=True, help="导出产物目录")
    ap.add_argument("--n", type=int, default=10)
    args = ap.parse_args()

    d = Path(args.model)
    fx = dict(np.load(d / "golden.npz"))

    img_feeds = {"pixel_values": fx["pixel_values"][:1]}
    txt_feeds = {"input_ids": fx["short_input_ids"], "attention_mask": fx["short_attention_mask"]}

    configs = [
        ("ORT CPU", None, None),
        ("ORT+OV GPU FP16", "GPU", "FP16"),
        ("ORT+OV GPU FP32", "GPU", "FP32"),
        ("ORT+OV CPU", "CPU", None),
    ]

    for enc, feeds, ref_key in (
        ("image_encoder.onnx", img_feeds, "image_embeds"),
        ("text_encoder.onnx", txt_feeds, "text_embeds"),
    ):
        print(f"\n=== {enc} ===", flush=True)
        ref = fx[ref_key][:1]
        for label, dev, prec in configs:
            t0 = time.time()
            try:
                sess = build(d / enc, dev, prec)
            except Exception as e:  # noqa: BLE001
                print(f"  {label}: BUILD FAILED {str(e)[:120]}", flush=True)
                continue
            load = time.time() - t0
            try:
                dt, out = bench(sess, feeds, label, args.n)
            except Exception as e:  # noqa: BLE001
                print(f"  {label}: RUN FAILED {str(e)[:120]}", flush=True)
                continue
            c = float(cos(out, ref)[0])
            print(f"           load={load:5.1f}s  cos_vs_torch={c:.8f}  "
                  f"{'OK' if c > 0.999 else 'DRIFT'}", flush=True)


if __name__ == "__main__":
    main()
