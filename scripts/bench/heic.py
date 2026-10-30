import time, glob
import numpy as np
from PIL import Image
import pillow_heif
pillow_heif.register_heif_opener()

print("pillow_heif", pillow_heif.__version__)
print("options:", [a for a in dir(pillow_heif.options) if not a.startswith("_")])
for a in ("DECODE_THREADS","THUMBNAIL_SIZE","SAVE_HDR_TO_12_BIT"):
    try: print("  ", a, "=", getattr(pillow_heif.options, a))
    except Exception: pass

heics = sorted(glob.glob("/tmp/bench/imgs/*.heic"))
p = heics[0]

def t(fn, label, n=4):
    fn()
    s = time.time()
    for _ in range(n): fn()
    print("%-30s %7.1f ms" % (label, (time.time() - s) / n * 1000), flush=True)

t(lambda: Image.open(p), "Image.open only")
t(lambda: Image.open(p).convert("RGB"), "open+convert RGB")
t(lambda: np.asarray(Image.open(p).convert("RGB")), "open+convert+asarray")
t(lambda: Image.open(p).convert("RGB").resize((224,224), Image.BICUBIC), "open+convert+resize224")

# multi-thread decode
for n in (1, 2, 4):
    pillow_heif.options.DECODE_THREADS = n
    t(lambda: np.asarray(Image.open(p).convert("RGB")), "DECODE_THREADS=%d" % n, n=3)
pillow_heif.options.DECODE_THREADS = 1

# embedded thumbnails
try:
    import pillow_heif
    f = pillow_heif.open_heif(p)
    print("top-level:", list(f.keys()) if hasattr(f, "keys") else "n/a")
    th = getattr(f, "thumbnails", None)
    print("thumbnails:", len(th) if th else 0)
    if th:
        for i, x in enumerate(th[:3]):
            print("  thumb", i, x.size if hasattr(x,"size") else type(x))
except Exception as e:
    print("thumb err:", type(e).__name__, e)
