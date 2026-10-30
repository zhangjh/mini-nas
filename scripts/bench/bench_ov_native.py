"""原生 OpenVINO 视觉编码器基准：正确性 + 各 batch 吞吐。

结论性脚本。要点：
- 用 dict 形式的 reshape（`model.reshape([1,3,224,224])` 会被当成 4 个输入的 shape）
- 每个配置都与 golden.npz 的 PyTorch 参考对余弦，速度再快、输出错了也没意义
- GPU 上静态 shape 才可靠，所以按 batch 分别编译并计入编译耗时
"""

import time

import numpy as np
import openvino as ov

D = "/tmp/bench/out/cn-clip-vit-b16"
fx = dict(np.load(f"{D}/golden.npz"))
X1 = fx["pixel_values"][:1].astype(np.float32)
REF = fx["image_embeds"]


def cos(a, b):
    a = a / np.clip(np.linalg.norm(a, axis=-1, keepdims=True), 1e-12, None)
    b = b / np.clip(np.linalg.norm(b, axis=-1, keepdims=True), 1e-12, None)
    return float((a * b).sum(-1).min())


core = ov.Core()
ONNX = f"{D}/image_encoder.onnx"
print("devices:", core.available_devices)

rows = []
for dev, prec in (("GPU", "f16"), ("GPU", "f32"), ("CPU", "f32")):
    for bs in (1, 4, 8, 16):
        m = core.read_model(ONNX)
        try:
            m.reshape({"pixel_values": [bs, 3, 224, 224]})
        except Exception as e:  # noqa: BLE001
            print(f"{dev} {prec} bs={bs}: reshape failed {str(e)[:100]}")
            continue
        cfg = {"INFERENCE_PRECISION_HINT": prec}
        if dev == "CPU":
            cfg["PERFORMANCE_HINT"] = "THROUGHPUT"
        try:
            t0 = time.time()
            c = core.compile_model(m, dev, cfg)
            load = time.time() - t0
            inp = np.repeat(X1, bs, axis=0) if bs > 1 else X1
            r = c([inp])  # warmup
            out = np.asarray(r[c.output(0)])
            n = 5 if bs <= 4 else 3
            t = time.time()
            for _ in range(n):
                out = np.asarray(c([inp])[c.output(0)])
            dt = (time.time() - t) / n
            # 与参考比：bs>1 时所有样本内容相同，逐样本取最小余弦
            cmin = min(cos(out[i:i + 1], REF[:1]) for i in range(bs))
            tag = "OK" if cmin > 0.999 else ("ZERO" if np.abs(out).max() < 1e-6 else "WRONG")
            print(f"OV {dev} {prec} bs={bs:2d}: {dt*1000:7.1f} ms/batch  "
                  f"{bs/dt:5.2f} img/s  {dt*1000/bs:6.1f} ms/img  "
                  f"load={load:5.1f}s  cos={cmin:.8f}  {tag}", flush=True)
            rows.append((dev, prec, bs, dt, load, cmin, tag))
        except Exception as e:  # noqa: BLE001
            print(f"OV {dev} {prec} bs={bs}: FAILED {str(e)[:130]}", flush=True)
    print()

print("=== summary ===")
for dev, prec, bs, dt, load, cmin, tag in rows:
    print(f"  {dev}/{prec} bs={bs:<2d} {dt*1000/bs:6.1f} ms/img  {tag}")
