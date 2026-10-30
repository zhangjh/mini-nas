"""判定 CN-CLIP 的正确预处理：直接拉伸 vs 短边缩放+中心裁剪。

背景：官方 preprocessor_config.json 是 do_center_crop=false + size 224x224，
即**直接拉伸**（宽高比失真）。而 OpenAI CLIP 与设计文档写的是「短边缩放 +
中心裁剪」。两者对 4:3 照片的差异是横向压扁 33%，会实质影响检索质量。

判定方法：拿官方仓库自带的 festival.jpg（真实照片，属于训练分布内），
用 6 条中文描述（3 条贴合画面、3 条明显无关）算图文相似度。
哪种预处理的「贴合 - 无关」间隔更大，哪种就更接近模型训练时的分布。
"""

import io
import time
import urllib.request

import numpy as np
import onnxruntime as ort
from PIL import Image

D = "/mnt/nas/dev/mini-nas/server/models/cn-clip-vit-b16"
MEAN = np.array([0.48145466, 0.4578275, 0.40821073], dtype=np.float32)
STD = np.array([0.26862954, 0.26130258, 0.27577711], dtype=np.float32)

MATCHED = ["节日庆典现场，人群聚集", "广场上有很多人在庆祝", "夜晚的灯光和人群"]
MISMATCHED = ["一只猫躺在沙发上", "雪山脚下的湖泊", "办公室里的电脑桌面"]


def load_image() -> Image.Image:
    url = ("https://www.modelscope.cn/api/v1/models/"
           "AI-ModelScope/chinese-clip-vit-base-patch16/repo?Revision=master&FilePath=festival.jpg")
    for attempt in range(3):
        try:
            data = urllib.request.urlopen(url, timeout=30).read()
            return Image.open(io.BytesIO(data)).convert("RGB")
        except Exception as e:  # noqa: BLE001
            print(f"  download attempt {attempt + 1} failed: {str(e)[:100]}")
            time.sleep(2)
    raise SystemExit("cannot download festival.jpg")


def prep_stretch(img: Image.Image) -> np.ndarray:
    """官方配置：直接 resize 到 224x224（BICUBIC），不裁剪。"""
    r = img.resize((224, 224), Image.BICUBIC)
    a = np.asarray(r, dtype=np.float32) / 255.0
    a = (a - MEAN) / STD
    return a.transpose(2, 0, 1)[None].astype(np.float32)


def prep_short_edge(img: Image.Image) -> np.ndarray:
    """OpenAI CLIP 风格：短边缩放到 224，再中心裁剪 224x224。"""
    w, h = img.size
    if w <= h:
        ow, oh = 224, int(224 * h / w)
    else:
        ow, oh = int(224 * w / h), 224
    r = img.resize((ow, oh), Image.BICUBIC)
    left = int(round((ow - 224) / 2.0))
    top = int(round((oh - 224) / 2.0))
    r = r.crop((left, top, left + 224, top + 224))
    a = np.asarray(r, dtype=np.float32) / 255.0
    a = (a - MEAN) / STD
    return a.transpose(2, 0, 1)[None].astype(np.float32)


def cos(a, b):
    a = a / np.linalg.norm(a, axis=-1, keepdims=True)
    b = b / np.linalg.norm(b, axis=-1, keepdims=True)
    return (a * b).sum(-1)


img = load_image()
print(f"source: {img.size[0]}x{img.size[1]} (aspect {img.size[0] / img.size[1]:.3f})")

img_sess = ort.InferenceSession(f"{D}/image_encoder.onnx", providers=["CPUExecutionProvider"])
txt_sess = ort.InferenceSession(f"{D}/text_encoder.onnx", providers=["CPUExecutionProvider"])

# 文本侧与预处理无关，先算一次
from transformers import AutoTokenizer  # noqa: E402

tok = AutoTokenizer.from_pretrained("/tmp/bench/cnclip-b16")
texts = MATCHED + MISMATCHED
tk = tok(texts, return_tensors="np", padding="max_length", truncation=True, max_length=64)
t_emb = np.concatenate([
    txt_sess.run(None, {"input_ids": tk["input_ids"][i:i + 1],
                        "attention_mask": tk["attention_mask"][i:i + 1]})[0]
    for i in range(len(texts))
])

results = {}
for name, fn in (("stretch(官方)", prep_stretch), ("short_edge+center", prep_short_edge)):
    x = fn(img)
    i_emb = img_sess.run(None, {"pixel_values": x})[0]
    sim = cos(i_emb, t_emb)
    matched, mismatched = sim[:3], sim[3:]
    margin = float(matched.mean() - mismatched.mean())
    results[name] = {"emb": i_emb, "sim": sim, "margin": margin}
    print(f"\n{name}:")
    for t, s in zip(texts, sim):
        tag = "贴合" if s in matched else "无关"
        print(f"  [{tag}] {s:<16} {s:.4f}")
    print(f"  matched mean={matched.mean():.4f}  mismatched mean={mismatched.mean():.4f}  "
          f"margin={margin:+.4f}")

a, b = results["stretch(官方)"], results["short_edge+center"]
print(f"\n两种预处理的图像向量余弦: {float(cos(a['emb'], b['emb'])[0]):.6f}")
print(f"margin 差值: {a['margin'] - b['margin']:+.4f}  "
      f"（>0 表示官方拉伸更优，<0 表示中心裁剪更优）")
