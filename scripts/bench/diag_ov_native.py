"""原生 OpenVINO 与 ORT-OpenVINO EP 在 GPU 上的正确性对照。

背景：ORT 的 OpenVINOExecutionProvider 设 device_type=GPU 后输出全零向量
（不是精度漂移，是 512 维全 0），CPU 路径则完全正确。需要区分三种可能：
  1. iGPU 根本跑不了（驱动/权限）
  2. ORT 的 OV EP 有 bug
  3. 动态 shape 在 OV GPU 上不成立

做法：同一个 ONNX 分别用 ORT-OV EP 和原生 OV API 跑，静态/动态 shape 各测一次，
都与 golden.npz 的 PyTorch 参考对余弦。
"""

import time

import numpy as np
import onnxruntime as ort
import openvino as ov

D = "/tmp/bench/out/cn-clip-vit-b16"
fx = dict(np.load(f"{D}/golden.npz"))
X = fx["pixel_values"][:1].astype(np.float32)
REF = fx["image_embeds"][:1]


def cos(a, b):
    a = a / np.clip(np.linalg.norm(a, axis=-1, keepdims=True), 1e-12, None)
    b = b / np.clip(np.linalg.norm(b, axis=-1, keepdims=True), 1e-12, None)
    return float((a * b).sum())


def report(tag, out, secs):
    n = float(np.linalg.norm(out))
    print(f"{tag:<46} {secs*1000:8.1f} ms  norm={n:9.4f}  cos={cos(out, REF):.8f}"
          f"  {'OK' if cos(out, REF) > 0.999 else ('ZERO' if n < 1e-6 else 'WRONG')}")


# ---------------------------------------------------------------- 0. 设备
core = ov.Core()
print("OV version:", ov.__version__)
print("available_devices:", core.available_devices)
for d in core.available_devices:
    try:
        print(f"  {d}: {core.get_property(d, 'FULL_DEVICE_NAME')}")
    except Exception as e:  # noqa: BLE001
        print(f"  {d}: <no FULL_DEVICE_NAME> {e}")
print()

# ---------------------------------------------------------------- 1. ORT EP 对照
print("--- ORT-OpenVINO EP ---")
for dev in ("CPU", "GPU"):
    try:
        so = ort.SessionOptions()
        so.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
        s = ort.InferenceSession(f"{D}/image_encoder.onnx", so,
                                 providers=["OpenVINOExecutionProvider"],
                                 provider_options=[{"device_type": dev, "precision": "FP16"}])
        s.run(None, {"pixel_values": X})
        t = time.time()
        out = s.run(None, {"pixel_values": X})[0]
        report(f"ORT-OV device={dev} FP16 (dynamic)", out, time.time() - t)
    except Exception as e:  # noqa: BLE001
        print(f"ORT-OV {dev}: FAILED {str(e)[:150]}")
print()

# ---------------------------------------------------------------- 2. 原生 OV
print("--- native OpenVINO ---")
base = core.read_model(f"{D}/image_encoder.onnx")
print("original input shape:", base.input(0).get_partial_shape())

for tag, shape in (("static 1x3x224x224", [1, 3, 224, 224]),
                   ("dynamic -1x3x224x224", [-1, 3, 224, 224])):
    m = core.read_model(f"{D}/image_encoder.onnx")
    try:
        m.reshape([shape])
    except Exception as e:  # noqa: BLE001
        print(f"{tag}: reshape failed {str(e)[:120]}")
        continue
    for dev, prec in (("GPU", "f16"), ("GPU", "f32"), ("CPU", "f32")):
        cfg = {}
        if prec == "f16":
            cfg["INFERENCE_PRECISION_HINT"] = "f16"
        try:
            t0 = time.time()
            c = core.compile_model(m, dev, cfg)
            load = time.time() - t0
            r = c([X])
            out = np.asarray(r[c.output(0)])
            # 再跑几次取稳态
            t = time.time()
            for _ in range(5):
                out = np.asarray(c([X])[c.output(0)])
            report(f"OV {dev} {prec} {tag} (load {load:.1f}s)", out, (time.time() - t) / 5)
        except Exception as e:  # noqa: BLE001
            print(f"OV {dev} {prec} {tag}: FAILED {str(e)[:150]}")
    print()

# ---------------------------------------------------------------- 3. 文本编码器
print("--- text encoder (native OV, static 1xL) ---")
tk_ids = fx["short_input_ids"].astype(np.int64)
tk_mask = fx["short_attention_mask"].astype(np.int64)
tref = fx["text_embeds"][:1]
L = tk_ids.shape[1]
tm = core.read_model(f"{D}/text_encoder.onnx")
print("original text input shapes:", [str(i.get_partial_shape()) for i in tm.inputs])
for dev, prec in (("GPU", "f16"), ("CPU", "f32")):
    m = core.read_model(f"{D}/text_encoder.onnx")
    try:
        m.reshape({"input_ids": [1, L], "attention_mask": [1, L]})
        cfg = {"INFERENCE_PRECISION_HINT": "f16"} if prec == "f16" else {}
        t0 = time.time()
        c = core.compile_model(m, dev, cfg)
        load = time.time() - t0
        feeds = {"input_ids": tk_ids, "attention_mask": tk_mask}
        out = np.asarray(c(feeds)[c.output(0)])
        t = time.time()
        for _ in range(5):
            out = np.asarray(c(feeds)[c.output(0)])
        secs = (time.time() - t) / 5
        c_ = cos(out, tref)
        n = float(np.linalg.norm(out))
        print(f"OV text {dev} {prec} (load {load:.1f}s){'':<20} {secs*1000:8.1f} ms  norm={n:9.4f}  cos={c_:.8f}"
              f"  {'OK' if c_ > 0.999 else ('ZERO' if n < 1e-6 else 'WRONG')}")
    except Exception as e:  # noqa: BLE001
        print(f"OV text {dev} {prec}: FAILED {str(e)[:150]}")
