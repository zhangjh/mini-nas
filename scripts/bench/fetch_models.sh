#!/bin/bash
# 从目标机分块拉取模型产物。
#
# 为什么要分块：SSH 走 Cloudflare Tunnel，access token 约 5 分钟过期，单条长连接
# 传大文件必断。16MB 一块，每块独立重试，断了只补那一块。
set -u

HOST="ssh.zhangjh.cn"
USER_="zhangjh"
SRC="/tmp/bench/out/cn-clip-vit-b16"
PARTS="/tmp/bench/out/parts"
LOCAL="/c/Users/cn-dantezhang/dev/mini-nas/server/models/cn-clip-vit-b16"
CHUNK=16

FILES="image_encoder.onnx text_encoder.onnx vocab.txt meta.json golden.npz verify.json"

ssh_retry() {
  local n=0
  while [ $n -lt 6 ]; do
    if ssh -o ConnectTimeout=20 -o ServerAliveInterval=15 "$HOST" -l "$USER_" "$@"; then
      return 0
    fi
    n=$((n + 1))
    sleep 3
  done
  return 1
}

# 远端切块（幂等：已存在就跳过）
ssh_retry "mkdir -p $PARTS && cd $PARTS && for f in image_encoder.onnx text_encoder.onnx; do
  if [ ! -f \${f}.part_aa ]; then
    split -b ${CHUNK}M -d -a 3 $SRC/\$f \${f}.part_
    echo \"split \$f -> \$(ls \${f}.part_* | wc -l) chunks\"
  else
    echo \"\$f already split: \$(ls \${f}.part_* | wc -l) chunks\"
  fi
done"

mkdir -p "$LOCAL"

for f in $FILES; do
  case "$f" in
    image_encoder.onnx|text_encoder.onnx)
      list=$(ssh_retry "ls $PARTS/${f}.part_*" | tr -d '\r')
      if [ -z "$list" ]; then echo "FATAL: no parts for $f"; exit 1; fi
      rm -f "$LOCAL/$f"
      for p in $list; do
        base=$(basename "$p")
        out="$LOCAL/.$base"
        want=$(ssh_retry "stat -c%s $p" | tr -d '\r')
        ok=0
        for try in 1 2 3 4 5 6 7 8; do
          ssh -o ConnectTimeout=20 "$HOST" -l "$USER_" "cat $p" > "$out" 2>/dev/null
          got=$(wc -c < "$out" 2>/dev/null || echo 0)
          if [ "$got" = "$want" ]; then ok=1; break; fi
          echo "  retry $base ($got/$want)"
          sleep 2
        done
        if [ "$ok" != "1" ]; then echo "FATAL: $base incomplete"; exit 1; fi
        cat "$out" >> "$LOCAL/$f"
        rm -f "$out"
        echo "  $f += $base ($want bytes)"
      done
      ;;
    *)
      ssh_retry "cat $SRC/$f" > "$LOCAL/$f" || { echo "FATAL: $f"; exit 1; }
      echo "  $f fetched"
      ;;
  esac
done

echo "=== md5 verify ==="
for f in $FILES; do
  a=$(ssh_retry "md5sum $SRC/$f | cut -d' ' -f1" | tr -d '\r')
  b=$(md5sum "$LOCAL/$f" | cut -d' ' -f1)
  if [ "$a" = "$b" ]; then echo "  OK   $f  $b"; else echo "  FAIL $f  remote=$a local=$b"; fi
done
echo "TRANSFER_DONE"
