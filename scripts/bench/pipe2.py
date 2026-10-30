import time, glob, threading, queue, numpy as np, onnxruntime as ort
from PIL import Image
import pillow_heif
pillow_heif.register_heif_opener()
MEAN = np.array([0.48145466, 0.4578275, 0.40821073], dtype=np.float32)
STD  = np.array([0.26862954, 0.26130258, 0.27577711], dtype=np.float32)

def preprocess(path):
    im = Image.open(path)
    if path.lower().endswith((".jpg", ".jpeg")):
        im.draft("RGB", (224, 224))
    im = im.convert("RGB").resize((224, 224), Image.BICUBIC)
    a = np.asarray(im, dtype=np.float32) / 255.0
    return np.ascontiguousarray(((a - MEAN) / STD).transpose(2, 0, 1))

so = ort.SessionOptions()
so.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
sess = ort.InferenceSession("/tmp/bench/m/vitb16.onnx", so,
                            providers=["OpenVINOExecutionProvider"],
                            provider_options=[{"device_type": "GPU", "precision": "FP16"}])
inp = sess.get_inputs()[0]

def run(files, ndec, bs, label):
    q = queue.Queue(maxsize=ndec * 6)
    def dec():
        for f in files: q.put(preprocess(f))
        q.put(None)
    ths = [threading.Thread(target=dec, daemon=True) for _ in range(ndec)]
    for th in ths: th.start()
    done = 0; buf = []; t0 = time.time()
    while True:
        it = q.get()
        if it is None:
            done += 1
            if done == ndec: break
            continue
        buf.append(it)
        if len(buf) >= bs:
            sess.run(None, {inp.name: np.stack(buf, 0)}); buf = []
    if buf: sess.run(None, {inp.name: np.stack(buf, 0)})
    el = time.time() - t0
    print("%-28s %6.2f img/s  %6.1f ms/img" % (label, len(files)/el, el*1000/len(files)), flush=True)

jpgs  = sorted(glob.glob("/tmp/bench/imgs/*.jpg"))
heics = sorted(glob.glob("/tmp/bench/imgs/*.heic"))
run(jpgs[:2], 1, 1, "warmup")
for ndec in (1, 2, 3):
    for bs in (1, 4):
        run(jpgs, ndec, bs, "JPEG dec=%d bs=%d" % (ndec, bs))
for ndec in (2, 4):
    run(heics * 3, ndec, 1, "HEIC dec=%d bs=1" % ndec)
