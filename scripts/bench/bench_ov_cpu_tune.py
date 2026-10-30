"""OpenVINO CPU 侧参数调优。

GPU 是主路径（98 ms/img），但必须有可用的 CPU 回退。原生 OV 默认配置在
4 核 Celeron 上只有 0.70 img/s，比 ORT CPU（722 ms）还慢一倍，需要确认
是 hint/线程数的问题还是内核本身的问题。
"""

import time

import numpy as np
import openvino as ov

D = "/tmp/bench/out/cn-clip-vit-b16"
fx = dict(np.load(f"{D}/golden.npz"))
X = fx["pixel_values"][:1].astype(np.float32)
REF = fx["image_embeds"][:1]

core = ov.Core()
ONNX = f"{D}/image_encoder.onnx"

configs = [
    ("default", {}),
    ("LATENCY", {"PERFORMANCE_HINT": "LATENCY"}),
    ("LATENCY + 4 threads", {"PERFORMANCE_HINT": "LATENCY", "INFERENCE_NUM_THREADS": 4}),
    ("LATENCY + 2 threads", {"PERFORMANCE_HINT": "LATENCY", "INFERENCE_NUM_THREADS": 2}),
    ("LATENCY + 1 thread", {"PERFORMANCE_HINT": "LATENCY", "INFERENCE_NUM_THREADS": 1}),
    ("THROUGHPUT + 1 stream", {"PERFORMANCE_HINT": "THROUGHPUT", "NUM_STREAMS": 1}),
    ("THROUGHPUT + 4 streams", {"PERFORMANCE_HINT": "THROUGHPUT", "NUM_STREAMS": 4}),
]

for tag, cfg in configs:
    m = core.read_model(ONNX)
    m.reshape({"pixel_values": [1, 3, 224, 224]})
    try:
        c = core.compile_model(m, "CPU", cfg)
        c([X])
        t = time.time()
        for _ in range(5):
            out = np.asarray(c([X])[c.output(0)])
        dt = (time.time() - t) / 5
        a = out / np.linalg.norm(out)
        b = REF / np.linalg.norm(REF)
        cs = float((a * b).sum())
        print(f"CPU {tag:<24} {dt*1000:7.1f} ms  {1/dt:5.2f} img/s  cos={cs:.8f}", flush=True)
    except Exception as e:  # noqa: BLE001
        print(f"CPU {tag}: FAILED {str(e)[:120]}", flush=True)
