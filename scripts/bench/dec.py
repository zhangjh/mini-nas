import time, glob, numpy as np
from PIL import Image
import pillow_heif
pillow_heif.register_heif_opener()
MEAN = np.array([0.48145466, 0.4578275, 0.40821073], dtype=np.float32)
STD  = np.array([0.26862954, 0.26130258, 0.27577711], dtype=np.float32)

def norm(im):
    im = im.convert("RGB").resize((224, 224), Image.BICUBIC)
    a = np.asarray(im, dtype=np.float32) / 255.0
    return np.ascontiguousarray(((a - MEAN) / STD).transpose(2, 0, 1))

def plain(p):
    return norm(Image.open(p))

def draft(p):
    im = Image.open(p)
    im.draft("RGB", (224, 224))   # JPEG DCT scaling
    return norm(im)

def bench(fn, files, label):
    fn(files[0])
    t = time.time()
    for f in files: fn(f)
    d = (time.time() - t) / len(files)
    print("%-22s %7.1f ms/img  %5.2f img/s" % (label, d * 1000, 1 / d), flush=True)

jpgs = sorted(glob.glob("/tmp/bench/imgs/*.jpg"))
big  = [f for f in jpgs if "p0" in f or "p1" in f]
heics = sorted(glob.glob("/tmp/bench/imgs/*.heic"))

# verify draft equivalence
a, b = plain(jpgs[0]), draft(jpgs[0])
print("draft max abs diff vs full: %.4f" % float(np.abs(a - b).max()), flush=True)

bench(plain, jpgs, "jpeg full-res")
bench(draft, jpgs, "jpeg draft(224)")
bench(plain, heics, "heic full-res")
bench(draft, heics, "heic draft(224)")
