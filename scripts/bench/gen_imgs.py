import os, numpy as np
from PIL import Image
os.makedirs("/tmp/bench/imgs", exist_ok=True)
rng = np.random.default_rng(0)
for i in range(24):
    w, h = (4032, 3024) if i % 2 == 0 else (1920, 1080)
    # smooth-ish photo-like content so JPEG is realistic in size
    base = rng.integers(0, 256, (h // 32, w // 32, 3), dtype=np.uint8)
    img = Image.fromarray(base).resize((w, h), Image.BICUBIC)
    img.save(f"/tmp/bench/imgs/p{i:02d}.jpg", quality=90)
print("jpeg done")
# HEIC sample via pillow-heif
import pillow_heif
pillow_heif.register_heif_opener()
src = Image.open("/tmp/bench/imgs/p00.jpg")
for i in range(4):
    src.save(f"/tmp/bench/imgs/h{i:02d}.heic", quality=85)
print("heic done")
