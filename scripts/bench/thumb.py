import time, glob, os, numpy as np, onnxruntime as ort
from PIL import Image
MEAN = np.array([0.48145466, 0.4578275, 0.40821073], dtype=np.float32)
STD  = np.array([0.26862954, 0.26130258, 0.27577711], dtype=np.float32)
TH = "/mnt/nas/dev/mini-nas/data/thumb_cache"

def prep_thumb(p):
    im = Image.open(p).convert("RGB")
    im = im.resize((224, 224), Image.BICUBIC)
    a = np.asarray(im, dtype=np.float32) / 255.0
    return np.ascontiguousarray(((a - MEAN) / STD).transpose(2, 0, 1))

thumbs = sorted(glob.glob(TH + "/*.jpg"))[:60]
print("thumbs:", len(thumbs))
szs = []
for p in thumbs[:20]:
    with Image.open(p) as im: szs.append(im.size)
print("sample sizes:", szs[:8])
print("min short side:", min(min(s) for s in szs), " avg bytes:", int(np.mean([os.path.getsize(p) for p in thumbs])))

# decode-only cost
prep_thumb(thumbs[0])
t = time.time()
for p in thumbs: prep_thumb(p)
d = (time.time() - t) / len(thumbs)
print("thumb decode+prep: %.1f ms/img (%.1f img/s)" % (d*1000, 1/d), flush=True)

so = ort.SessionOptions()
so.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
sess = ort.InferenceSession("/tmp/bench/m/vitb16.onnx", so,
    providers=["OpenVINOExecutionProvider"],
    provider_options=[{"device_type": "GPU", "precision": "FP16"}])
inp = sess.get_inputs()[0]

for _ in range(2): sess.run(None, {inp.name: prep_thumb(thumbs[0])[None]})
t = time.time()
for p in thumbs:
    sess.run(None, {inp.name: prep_thumb(p)[None]})
e = (time.time() - t) / len(thumbs)
print("END-TO-END from thumbnail: %.1f ms/img (%.2f img/s) -> 14k in %.1f min" % (e*1000, 1/e, 13978*e/60), flush=True)
