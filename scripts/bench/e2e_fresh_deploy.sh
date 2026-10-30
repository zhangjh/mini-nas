#!/usr/bin/env bash
# 全新部署端到端验证：默认挂载是否让首次上传可用。
# 在目标机执行，验证后自动清理。
set -uo pipefail

ROOT=/tmp/vsrv
BIN=$ROOT/mini-nas-server
DATA=$ROOT/data
PORT=18080
BASE=http://127.0.0.1:$PORT

rm -rf "$DATA" "$ROOT/config.json"
mkdir -p "$DATA"

cat > "$ROOT/config.json" <<'JSON'
{
  "port": 18080,
  "data_dir": "/tmp/vsrv/data",
  "upload_dir": "/tmp/vsrv/data/files",
  "jwt_secret": "0123456789abcdef0123456789abcdef",
  "password": "testpass"
}
JSON

# HOST_ROOT 故意设为 /host，验证本地挂载不会被错误前缀
export HOST_ROOT=/host

"$BIN" -config "$ROOT/config.json" -data "$DATA" > "$ROOT/server.log" 2>&1 &
PID=$!
trap 'kill $PID 2>/dev/null; wait $PID 2>/dev/null' EXIT

for i in $(seq 1 40); do
  if curl -fsS "$BASE/api/health" >/dev/null 2>&1; then break; fi
  sleep 0.25
done

echo "=== 启动日志（挂载相关）==="
grep -iE "mount|Default upload" "$ROOT/server.log" || echo "（无）"

echo
echo "=== 挂载表 ==="
curl -sS "$BASE/api/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"tester","password":"pass1234"}' > "$ROOT/reg.json" 2>/dev/null
TOKEN=$(python3 -c "import json;print(json.load(open('$ROOT/reg.json')).get('data',{}).get('token',''))" 2>/dev/null)

if [ -z "$TOKEN" ]; then
  curl -sS "$BASE/api/auth/login" -H 'Content-Type: application/json' \
    -d '{"username":"tester","password":"pass1234"}' > "$ROOT/login.json"
  TOKEN=$(python3 -c "import json;print(json.load(open('$ROOT/login.json')).get('data',{}).get('token',''))")
fi
echo "token 长度: ${#TOKEN}"

curl -sS "$BASE/api/mounts" -H "Authorization: Bearer $TOKEN" | python3 -m json.tool

echo
echo "=== 上传到默认挂载 files/ ==="
echo "hello-from-fresh-deploy" > "$ROOT/probe.txt"
curl -sS -X POST "$BASE/api/files/upload?path=files" \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@$ROOT/probe.txt" | python3 -m json.tool

echo
echo "=== 落盘检查 ==="
find "$DATA" -type f | sort
echo "--- 内容 ---"
cat "$DATA/files/probe.txt" 2>/dev/null || echo "（文件不存在）"

echo
echo "=== 目录列表 ==="
curl -sS "$BASE/api/files?path=files" -H "Authorization: Bearer $TOKEN" | python3 -m json.tool
