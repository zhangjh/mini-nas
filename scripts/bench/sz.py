import glob, collections
from PIL import Image
TH = "/mnt/nas/dev/mini-nas/data/thumb_cache"
files = sorted(glob.glob(TH + "/*.jpg"))
print("total:", len(files))
short = collections.Counter(); ratios = collections.Counter()
lt224 = 0; n = 0
for p in files:
    try:
        with Image.open(p) as im: w, h = im.size
    except Exception: continue
    n += 1
    s = min(w, h)
    short[s // 32 * 32] += 1
    if s < 224: lt224 += 1
print("sampled:", n)
print("short-side histogram (bucket=32):", dict(sorted(short.items())))
print("short side < 224: %d (%.1f%%)" % (lt224, lt224 * 100.0 / n))
