"""OpenVINO GPU/CPU 推理基准：batch size 扫描。

在目标机上运行（必须经 sg render 切换组，否则 GPU 不可见、静默回退 CPU）：

    sg render -c "/tmp/bench/bin/python scripts/bench/bench_gpu_sweep.py"

前置：
    python3 -m venv /tmp/bench
    /tmp/bench/bin/pip install onnxruntime-openvino numpy
    # 模型放到 /tmp/bench/m/vitb16.onnx
"""

import time

import numpy as np
import onnxruntime as ort

MODEL = "/tmp/bench/m/vitb16.onnx"


def build(device: str, precision: str = "FP16"):
    so = ort.SessionOptions()
    so.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
    opts = {"device_type": device}
    if precision:
        opts["precision"] = precision
    return ort.InferenceSession(
        MODEL, so,
        providers=["OpenVINOExecutionProvider"],
        provider_options=[opts],
    )


def sweep(sess, label: str):
    inp = sess.get_inputs()[0]
    print(f"[{label}] input={inp.name} shape={inp.shape} type={inp.type}", flush=True)
    for bs in (1, 2, 4, 8, 16, 32):
        x = np.random.rand(bs, 3, 224, 224).astype(np.float32)
        try:
            sess.run(None, {inp.name: x})  # warmup
            n = 5 if bs <= 4 else 3
            t = time.time()
            for _ in range(n):
                sess.run(None, {inp.name: x})
            dt = (time.time() - t) / n
            print(
                f"  {label} bs={bs:2d}: {dt * 1000:7.0f} ms/batch  "
                f"{bs / dt:6.2f} img/s  {dt * 1000 / bs:6.1f} ms/img",
                flush=True,
            )
        except Exception as e:  # noqa: BLE001
            print(f"  {label} bs={bs}: FAILED {str(e)[:160]}", flush=True)


if __name__ == "__main__":
    for dev, prec in (("GPU", "FP16"), ("CPU", "")):
        try:
            t0 = time.time()
            sess = build(dev, prec)
            load = time.time() - t0
            print(f"[{dev}] session loaded in {load:.1f}s", flush=True)
            sweep(sess, dev)
        except Exception as e:  # noqa: BLE001
            print(f"[{dev}] FAILED: {str(e)[:200]}", flush=True)
