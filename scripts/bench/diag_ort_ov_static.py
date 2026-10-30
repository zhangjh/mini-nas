"""ORT-OpenVINO EP 在静态 shape 下是否恢复正常。

之前 ORT-OV GPU 输出全零/错误，模型是动态 batch 轴。若换成静态 shape 后
输出正确，就可以只绑一套 ORT C API（CPU 722ms + GPU ~100ms），不必同时
引入 OpenVINO C API；否则必须走原生 OV。
"""

import time

import numpy as np
import onnx
import onnxruntime as ort
from onnx import TensorProto

D = "/tmp/bench/out/cn-clip-vit-b16"
fx = dict(np.load(f"{D}/golden.npz"))
REF = fx["image_embeds"][:1]


def cos(a, b):
    a = a / np.clip(np.linalg.norm(a, axis=-1, keepdims=True), 1e-12, None)
    b = b / np.clip(np.linalg.norm(b, axis=-1, keepdims=True), 1e-12, None)
    return float((a * b).sum())


def make_static(src, dst, batch):
    m = onnx.load(src)
    for inp in m.graph.input:
        dims = inp.type.tensor_type.shape.dim
        if inp.name == "pixel_values":
            dims[0].ClearField("dim_param")
            dims[0].dim_value = batch
            for i in (1, 2, 3):
                dims[i].ClearField("dim_param")
    for out in m.graph.output:
        dims = out.type.tensor_type.shape.dim
        dims[0].ClearField("dim_param")
        dims[0].dim_value = batch
    onnx.save(m, dst)
    return dst


for bs in (1, 16):
    path = f"/tmp/bench/img_static{bs}.onnx"
    make_static(f"{D}/image_encoder.onnx", path, bs)
    X = np.repeat(fx["pixel_values"][:1], bs, axis=0).astype(np.float32)
    print(f"=== static batch={bs} ===", flush=True)
    for dev, prec in (("GPU", "FP16"), ("CPU", None)):
        try:
            so = ort.SessionOptions()
            so.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
            opts = {"device_type": dev}
            if prec:
                opts["precision"] = prec
            s = ort.InferenceSession(path, so, providers=["OpenVINOExecutionProvider"],
                                     provider_options=[opts])
            s.run(None, {"pixel_values": X})
            t = time.time()
            out = s.run(None, {"pixel_values": X})[0]
            dt = time.time() - t
            c = min(cos(out[i:i + 1], REF) for i in range(bs))
            n = float(np.abs(out).max())
            tag = "OK" if c > 0.999 else ("ZERO" if n < 1e-6 else "WRONG")
            print(f"  ORT-OV {dev} {prec or 'FP32'} bs={bs}: {dt*1000/bs:6.1f} ms/img  "
                  f"cos={c:.8f}  {tag}", flush=True)
        except Exception as e:  # noqa: BLE001
            print(f"  ORT-OV {dev} bs={bs}: FAILED {str(e)[:140]}", flush=True)
    print()
