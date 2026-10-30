#!/bin/bash
# 一键复现 CN-CLIP ONNX 模型产物。
#
# 模型不入库（两份 ONNX 合计 718MB，GitHub 免费 LFS 只有 1GB 存储 / 1GB 月流量），
# 用本脚本复现。产物落在 server/models/cn-clip-vit-b16/，该目录已被 .gitignore 忽略。
#
# 用法：
#   scripts/setup_model.sh                    # 默认下载到 /tmp，导出到 server/models
#   SRC_DIR=/data/weights scripts/setup_model.sh
#   PY=/tmp/bench/bin/python scripts/setup_model.sh   # 用已有 venv
#
# 依赖（首次）：
#   pip install torch --index-url https://download.pytorch.org/whl/cpu
#   pip install transformers onnx onnxruntime
#
# 为什么走 ModelScope：目标机上 huggingface.co 不可达，hf-mirror.com 也能连通但
# 大文件下载会卡住。modelscope.cn 实测 7MB/s，718MB 约 100 秒。
set -euo pipefail

MODEL_ID="${MODEL_ID:-AI-ModelScope/chinese-clip-vit-base-patch16}"
SRC_DIR="${SRC_DIR:-/tmp/cnclip-b16}"
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT_DIR="${OUT_DIR:-$HERE/../server/models/cn-clip-vit-b16}"
PY="${PY:-python3}"

# 文件名与期望大小下限（字节）。用于判断已有文件是否完整，避免半截文件被复用。
FILES="config.json:2000 preprocessor_config.json:200 vocab.txt:100000 pytorch_model.bin:700000000"

echo "== 1/4 下载 PyTorch 权重 =="
mkdir -p "$SRC_DIR"
for spec in $FILES; do
  f="${spec%%:*}"
  want="${spec##*:}"
  have=0
  [ -f "$SRC_DIR/$f" ] && have=$(wc -c < "$SRC_DIR/$f")
  if [ "$have" -ge "$want" ]; then
    echo "  skip $f ($have bytes)"
    continue
  fi
  echo "  fetch $f"
  curl -fL --retry 3 --retry-delay 2 --connect-timeout 20 \
    "https://www.modelscope.cn/api/v1/models/$MODEL_ID/repo?Revision=master&FilePath=$f" \
    -o "$SRC_DIR/$f.part"
  mv "$SRC_DIR/$f.part" "$SRC_DIR/$f"
  echo "  got $f ($(wc -c < "$SRC_DIR/$f") bytes)"
done

echo "== 2/4 检查依赖 =="
if ! "$PY" -c "import torch, transformers, onnx, onnxruntime" 2>/dev/null; then
  echo "缺少依赖，请先执行：" >&2
  echo "  pip install torch --index-url https://download.pytorch.org/whl/cpu" >&2
  echo "  pip install transformers onnx onnxruntime" >&2
  exit 1
fi
"$PY" -c "import transformers; print('  transformers', transformers.__version__)"

echo "== 3/4 导出 + 四段校验 =="
echo "  out: $OUT_DIR"
"$PY" "$HERE/export_clip_onnx.py" --model "$SRC_DIR" --out "$OUT_DIR" "$@"

echo "== 4/4 生成预处理黄金样本 =="
exec "$PY" "$HERE/make_preproc_golden.py" --model "$SRC_DIR" \
  --out "$OUT_DIR/preproc_golden.npz"
