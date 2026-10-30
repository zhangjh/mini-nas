import time, glob, io, numpy as np
from PIL import Image
TH = "/mnt/nas/dev/mini-nas/data/thumb_cache"
SRC = "/mnt/nas/照片"

thumbs = sorted(glob.glob(TH + "/*.jpg"))[:60]
bigs = sorted(glob.glob("/tmp/bench/imgs/*.jpg"))[:20]

def t(fn, label, n):
    fn()
    s = time.time()
    for _ in range(n): fn()
    print("%-34s %7.1f ms" % (label, (time.time()-s)/n*1000), flush=True)

# --- thumbnail: raw file read ---
t(lambda: [open(p,"rb").read() for p in thumbs], "thumb: open+read bytes", len(thumbs))

# --- thumbnail: decode from memory ---
blobs = [open(p,"rb").read() for p in thumbs]
def dec_mem(b):
    return np.asarray(Image.open(io.BytesIO(b)).convert("RGB").resize((224,224), Image.BICUBIC))
t(lambda: [dec_mem(b) for b in blobs], "thumb: decode from memory", len(blobs))

# --- thumbnail: open+decode from disk ---
def dec_disk(p):
    return np.asarray(Image.open(p).convert("RGB").resize((224,224), Image.BICUBIC))
t(lambda: [dec_disk(p) for p in thumbs], "thumb: decode from disk", len(thumbs))

# --- 12MP jpeg (local ext4): read vs decode ---
t(lambda: [open(p,"rb").read() for p in bigs], "12MP jpeg: open+read bytes", len(bigs))
bb = [open(p,"rb").read() for p in bigs]
def dec_big_mem(b):
    im = Image.open(io.BytesIO(b)); im.draft("RGB",(224,224))
    return np.asarray(im.convert("RGB").resize((224,224), Image.BICUBIC))
t(lambda: [dec_big_mem(b) for b in bb], "12MP jpeg: draft decode from memory", len(bb))

# --- real source photos on NAS (untouched by draft) ---
real = [p for p in sorted(glob.glob(SRC + "/**/*.jpg", recursive=True))[:30]]
if real:
    print("real src sample:", real[0])
    t(lambda: [open(p,"rb").read() for p in real], "NAS src: open+read bytes", len(real))
    rb = [open(p,"rb").read() for p in real]
    print("  avg MB: %.2f" % (np.mean([len(b) for b in rb])/1e6))
    def dec_r(b):
        im = Image.open(io.BytesIO(b)); im.draft("RGB",(224,224))
        return np.asarray(im.convert("RGB").resize((224,224), Image.BICUBIC))
    t(lambda: [dec_r(b) for b in rb], "NAS src: draft decode from memory", len(rb))
