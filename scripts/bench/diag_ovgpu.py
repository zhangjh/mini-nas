"""诊断 OpenVINO GPU 路径输出错误。

对比同一输入下 CPU 与 GPU 的输出分布：是否全零、是否 NaN、是否数值尺度异常、
是否只有部分元素错乱。这决定了是「精度问题」还是「算子实现 bug」。
"""

import numpy as np
import onnxruntime as ort

D = "/tmp/bench/out/cn-clip-vit-b16"
fx = dict(np.load(f"{D}/golden.npz"))


def build(model, dev=None, prec=None, opt_level=None):
    so = ort.SessionOptions()
    so.graph_optimization_level = (opt_level if opt_level is not None
                                   else ort.GraphOptimizationLevel.ORT_ENABLE_ALL)
    if dev is None:
        return ort.InferenceSession(model, so, providers=["CPUExecutionProvider"])
    o = {"device_type": dev}
    if prec:
        o["precision"] = prec
    return ort.InferenceSession(model, so, providers=["OpenVINOExecutionProvider"], provider_options=[o])


def stats(tag, a, ref):
    n = np.linalg.norm(a, axis=-1)
    rn = np.linalg.norm(ref, axis=-1)
    cn = a / np.clip(n[:, None], 1e-12, None)
    cr = ref / np.clip(rn[:, None], 1e-12, None)
    print(f"{tag}")
    print(f"  shape={a.shape} dtype={a.dtype}")
    print(f"  nan={np.isnan(a).sum()} inf={np.isinf(a).sum()} zero_elems={(a == 0).sum()}/{a.size}")
    print(f"  absmax={np.abs(a).max():.6g} mean={a.mean():.6g} std={a.std():.6g}")
    print(f"  norm={n}  ref_norm={rn}")
    print(f"  cos={(cn * cr).sum(-1)}")
    print(f"  first5={a.reshape(a.shape[0], -1)[0][:5]}")
    print(f"  ref5  ={ref.reshape(ref.shape[0], -1)[0][:5]}")


def run(model, feeds, ref, label, dev, prec):
    try:
        s = build(model, dev, prec)
        out = s.run(None, feeds)[0]
    except Exception as e:  # noqa: BLE001
        print(f"{label}: FAILED {type(e).__name__}: {str(e)[:200]}\n")
        return
    stats(label, out, ref)
    print()


print("########## image_encoder (new export) ##########")
img_feeds = {"pixel_values": fx["pixel_values"][:1]}
iref = fx["image_embeds"][:1]
stats("CPU baseline", build(f"{D}/image_encoder.onnx").run(None, img_feeds)[0], iref)
print()
run(f"{D}/image_encoder.onnx", img_feeds, iref, "GPU FP16", "GPU", "FP16")
run(f"{D}/image_encoder.onnx", img_feeds, iref, "GPU FP32", "GPU", "FP32")
run(f"{D}/image_encoder.onnx", {"pixel_values": fx["pixel_values"]}, fx["image_embeds"],
    "GPU FP16 batch=2 (dynamic axis)", "GPU", "FP16")

print("########## old vitb16.onnx (was used for the 111ms figure) ##########")
try:
    oref = build("/tmp/bench/m/vitb16.onnx").run(None, img_feeds)[0]
    print(f"old model CPU out shape={oref.shape}, cos_vs_new_cpu={float((oref[0]/np.linalg.norm(oref[0])) @ (iref[0]/np.linalg.norm(iref[0]))):.6f}")
    run("/tmp/bench/m/vitb16.onnx", img_feeds, oref, "old GPU FP16", "GPU", "FP16")
except Exception as e:  # noqa: BLE001
    print("old model unavailable:", str(e)[:200])
