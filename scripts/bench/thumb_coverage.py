"""精确统计缩略图覆盖率与缓存键碰撞风险。

缩略图缓存键规则（现有实现）：
    name = strings.ReplaceAll(filePath, "/", "_")
    cache = CacheDir/name + ".jpg"

其中 filePath 是容器内路径（/host/mnt/nas/...）。
碰撞条件：不同路径映射到同一个 name，例如
    /host/a/b.jpg  ->  _host_a_b.jpg
    /host/a_b.jpg  ->  _host_a_b.jpg
"""

import collections
import os

SRC = "/mnt/nas/照片"
CACHE = "/mnt/nas/dev/mini-nas/data/thumb_cache"
PREFIX = "_host"  # 容器内 /host 对应宿主 /


def cache_name(host_path: str) -> str:
    # 宿主路径 -> 容器内虚拟路径
    inner = "/host" + host_path
    return inner.replace("/", "_") + ".jpg"


def main():
    imgs = []
    for root, _, files in os.walk(SRC):
        for f in files:
            if f.lower().endswith((".jpg", ".jpeg", ".png", ".webp", ".bmp", ".gif")):
                imgs.append(os.path.join(root, f))

    cached = set(os.listdir(CACHE))
    print(f"images={len(imgs)}  cache_files={len(cached)}")

    missing = 0
    have = 0
    keys = collections.Counter()
    for p in imgs:
        n = cache_name(p)
        keys[n] += 1
        if n in cached:
            have += 1
        else:
            missing += 1

    print(f"have_thumb={have} ({have * 100.0 / len(imgs):.1f}%)")
    print(f"missing   ={missing} ({missing * 100.0 / len(imgs):.1f}%)")

    collide = {k: v for k, v in keys.items() if v > 1}
    print(f"colliding keys={len(collide)}  affected paths={sum(collide.values())}")
    for k, v in list(collide.items())[:5]:
        print(f"  {k} <- {v} paths")

    # 含下划线的文件名是碰撞的必要条件
    underscore = [p for p in imgs if "_" in os.path.basename(p)]
    print(f"basenames containing '_': {len(underscore)}")
    for p in underscore[:5]:
        print(f"  {p}")


if __name__ == "__main__":
    main()
