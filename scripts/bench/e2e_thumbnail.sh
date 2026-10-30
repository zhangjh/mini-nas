#!/usr/bin/env bash
# 缩略图子系统端到端验证：生成 / mtime 失效 / 改名失效 / 批量补齐 / 孤儿清理。
# 在目标机执行，验证后自动清理。
set -uo pipefail

ROOT=/tmp/vsrv
BIN=$ROOT/mini-nas-server
DATA=$ROOT/data2
PICS=$ROOT/pics
PORT=18081
BASE=http://127.0.0.1:$PORT
PY=/tmp/bench/bin/python

rm -rf "$DATA" "$PICS" "$ROOT/config2.json"
mkdir -p "$DATA" "$PICS"

# 造 6 张不同尺寸的测试图
$PY - <<'PY'
import os
from PIL import Image
rng = __import__("random")
for i, (w, h) in enumerate([(4000,3000),(1920,1080),(800,600),(640,480),(300,200),(1200,900)]):
    Image.new("RGB", (w, h), (i*40 % 256, 80, 160)).save(f"/tmp/vsrv/pics/p{i}.jpg", quality=90)
print("images:", sorted(os.listdir("/tmp/vsrv/pics")))
PY

cat > "$ROOT/config2.json" <<'JSON'
{
  "port": 18081,
  "data_dir": "/tmp/vsrv/data2",
  "upload_dir": "/tmp/vsrv/data2/files",
  "jwt_secret": "0123456789abcdef0123456789abcdef",
  "password": "testpass",
  "thumbnail": { "max_size": 320, "jpeg_quality": 80, "validate_mtime": true,
                 "backfill_workers": 4, "prune_orphans": true }
}
JSON

"$BIN" -config "$ROOT/config2.json" -data "$DATA" > "$ROOT/server2.log" 2>&1 &
PID=$!
trap 'kill $PID 2>/dev/null; wait $PID 2>/dev/null' EXIT

for i in $(seq 1 40); do curl -fsS "$BASE/api/health" >/dev/null 2>&1 && break; sleep 0.25; done

curl -sS "$BASE/api/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"t2","password":"pass1234"}' > "$ROOT/r2.json" 2>/dev/null
TOKEN=$($PY -c "import json;print(json.load(open('$ROOT/r2.json')).get('data',{}).get('token',''))")
[ -z "$TOKEN" ] && curl -sS "$BASE/api/auth/login" -H 'Content-Type: application/json' \
  -d '{"username":"t2","password":"pass1234"}' > "$ROOT/l2.json" && \
  TOKEN=$($PY -c "import json;print(json.load(open('$ROOT/l2.json')).get('data',{}).get('token',''))")
AUTH="Authorization: Bearer $TOKEN"

echo "=== 注册挂载 pics ==="
curl -sS -X POST "$BASE/api/mounts" -H "$AUTH" -H 'Content-Type: application/json' \
  -d "{\"name\":\"pics\",\"path\":\"$PICS\"}" | head -c 200; echo

CACHE=$DATA/thumb_cache
echo
echo "=== 1) 按需生成 ==="
for i in 0 1 2; do
  code=$(curl -sS -o "$ROOT/t$i.jpg" -w '%{http_code}' "$BASE/api/thumb?path=pics/p$i.jpg" -H "$AUTH")
  printf "  p%s.jpg -> HTTP %s, %s bytes\n" "$i" "$code" "$(stat -c%s "$ROOT/t$i.jpg" 2>/dev/null || echo 0)"
done
echo "  缓存文件数: $(ls "$CACHE" 2>/dev/null | wc -l)"
echo "  文件名样例: $(ls "$CACHE" 2>/dev/null | head -2 | tr '\n' ' ')"
echo "  尺寸检查:"; for i in 0 1 2; do printf "    t%s: " "$i"; $PY -c "from PIL import Image;print(Image.open('$ROOT/t$i.jpg').size)"; done

echo
echo "=== 2) mtime 失效（同路径换内容）==="
BEFORE=$(md5sum "$CACHE"/* 2>/dev/null | sort | md5sum | cut -d' ' -f1)
sleep 1.1
$PY -c "from PIL import Image; Image.new('RGB',(4000,3000),(255,0,0)).save('$PICS/p0.jpg', quality=90)"
curl -sS -o "$ROOT/t0b.jpg" "$BASE/api/thumb?path=pics/p0.jpg" -H "$AUTH"
AFTER=$(md5sum "$CACHE"/* 2>/dev/null | sort | md5sum | cut -d' ' -f1)
if [ "$BEFORE" != "$AFTER" ]; then echo "  [OK  ] 缓存已重建"; else echo "  [FAIL] 仍返回旧缓存"; fi
if [ "$(md5sum < "$ROOT/t0.jpg")" != "$(md5sum < "$ROOT/t0b.jpg")" ]; then
  echo "  [OK  ] 返回内容已变化"; else echo "  [FAIL] 返回内容未变"; fi

echo
echo "=== 3) 改名失效（旧键删除 + 新键创建，总数不变）==="
N0=$(ls "$CACHE" | wc -l)
curl -sS -X PUT "$BASE/api/file/rename" -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"path":"pics/p1.jpg","new_name":"p1b.jpg"}' > /dev/null
N1=$(ls "$CACHE" | wc -l)
code=$(curl -sS -o "$ROOT/t1b.jpg" -w '%{http_code}' "$BASE/api/thumb?path=pics/p1b.jpg" -H "$AUTH")
N2=$(ls "$CACHE" | wc -l)
echo "  改名后取缩略图: HTTP $code"
echo "  缓存数 改名前后=$N0 -> $N1（旧键应已删，-1）-> 取新图后=$N2（新键 +1）"
if [ "$N1" -eq "$((N0 - 1))" ]; then echo "  [OK  ] 旧缓存已随改名清除"; else echo "  [FAIL] 旧缓存未清除"; fi

echo
echo "=== 4) 批量补齐 ==="
curl -sS -X POST "$BASE/api/thumb/backfill" -H "$AUTH" -H 'Content-Type: application/json' -d '{}' | head -c 160; echo
for i in $(seq 1 60); do
  st=$(curl -sS "$BASE/api/thumb/status" -H "$AUTH")
  running=$($PY -c "import json,sys;d=json.loads(sys.argv[1])['data'];print(d['running']['state'] if d.get('running') else 'none')" "$st")
  [ "$running" = "none" ] || [ "$running" = "DONE" ] || [ "$running" = "FAILED" ] && break
  sleep 0.5
done
echo "$st" | $PY -m json.tool

echo
echo "=== 5a) 经 API 删除 → 立即失效 ==="
N0=$(ls "$CACHE" | wc -l)
curl -sS -X DELETE "$BASE/api/file" -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"path":"pics/p2.jpg"}' > /dev/null
N1=$(ls "$CACHE" | wc -l)
echo "  缓存数 $N0 -> $N1"
if [ "$N1" -eq "$((N0 - 1))" ]; then echo "  [OK  ] 删除后缓存立即清除"; else echo "  [FAIL] 缓存未清除"; fi

echo
echo "=== 5b) 绕过 API 直接删文件 → 由 backfill 清理孤儿 ==="
N0=$(ls "$CACHE" | wc -l)
rm -f "$PICS/p3.jpg"
echo "  直接 rm p3.jpg，缓存数仍为 $N0（尚无失效机制介入）"
curl -sS -X POST "$BASE/api/thumb/backfill" -H "$AUTH" -H 'Content-Type: application/json' -d '{}' > /dev/null
for i in $(seq 1 40); do
  running=$(curl -sS "$BASE/api/thumb/status" -H "$AUTH" | $PY -c "import json,sys;d=json.load(sys.stdin)['data'];print(d['running']['state'] if d.get('running') else 'none')")
  [ "$running" = "none" ] || [ "$running" = "DONE" ] && break
  sleep 0.5
done
N1=$(ls "$CACHE" | wc -l)
echo "  补齐后缓存数 $N0 -> $N1"
if [ "$N1" -lt "$N0" ]; then echo "  [OK  ] 孤儿缓存已被清理"; else echo "  [FAIL] 孤儿未清理"; fi

echo
echo "=== 服务端日志（错误）==="
grep -iE "error|failed|panic" "$ROOT/server2.log" | head -5 || echo "  （无）"
