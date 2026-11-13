#!/usr/bin/env bash
# 业务冒烟：发布 → 提交 → 批阅 → 汇总，覆盖一次正常链路与两个拒绝路径。
# 依赖：curl、python3（解析 JSON），在仓库根目录执行。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
PORT="${PORT:-18080}"
BASE="http://127.0.0.1:${PORT}"
DB="$(mktemp -u "${TMPDIR:-/tmp}/smoke-XXXXXX.db")"
COOKIES="$(mktemp -d)"

cleanup() {
  if [ -n "${PID:-}" ]; then kill "$PID" 2>/dev/null || true; fi
  rm -rf "$COOKIES" "$DB" "$DB"-wal "$DB"-shm
}
trap cleanup EXIT

echo "== 构建 =="
go build -o bin/server ./cmd/server

echo "== 启动 =="
PORT="$PORT" DB_PATH="$DB" SEED=1 TEACHING_CLOCK_OFFSET=0 ./bin/server &
PID=$!
for _ in $(seq 1 40); do
  if curl -fsS "$BASE/login" >/dev/null 2>&1; then break; fi
  sleep 0.25
done

login() { # $1=user, $2=jar
  curl -sS -o /dev/null -c "$2" -d "user_id=$1&password=demo1234" "$BASE/login"
}
teacher="$COOKIES/t.txt"; assistant="$COOKIES/a.txt"; student="$COOKIES/s.txt"
login T01 "$teacher"; login A01 "$assistant"; login S01 "$student"

json() { python3 -c "import json,sys; d=json.load(sys.stdin); print(d$1)"; }

deadline=$(python3 - <<'PY'
import datetime
print((datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(hours=48)).strftime('%Y-%m-%dT%H:%M:%SZ'))
PY
)

echo "== 发布 =="
aid=$(curl -sS -b "$teacher" -H 'Content-Type: application/json' \
  -d "{\"title\":\"CI smoke\",\"requirement\":\"answer\",\"deadline_at\":\"$deadline\"}" \
  "$BASE/api/assignments" | json "['id']")
code=$(curl -sS -o /dev/null -w '%{http_code}' -b "$teacher" -X POST "$BASE/api/assignments/$aid/publish")
[ "$code" = "200" ] || { echo "发布失败 HTTP $code"; exit 1; }
echo "assignment=$aid publish=$code"

echo "== 提交 =="
v1=$(curl -sS -b "$student" -H 'Content-Type: application/json' \
  -d '{"content":"smoke answer","request_id":"smoke-v1","base_version":0}' \
  "$BASE/api/assignments/$aid/submissions" | json "['id']")
echo "version=$v1"

replay=$(curl -sS -o /dev/null -w '%{http_code}' -b "$student" -H 'Content-Type: application/json' \
  -d '{"content":"smoke answer","request_id":"smoke-v1","base_version":0}' \
  "$BASE/api/assignments/$aid/submissions")
[ "$replay" = "200" ] || { echo "幂等重放应 200，实际 $replay"; exit 1; }

stale=$(curl -sS -o /dev/null -w '%{http_code}' -b "$student" -H 'Content-Type: application/json' \
  -d '{"content":"old page","request_id":"smoke-stale","base_version":0}' \
  "$BASE/api/assignments/$aid/submissions")
[ "$stale" = "409" ] || { echo "旧版本提交应 409，实际 $stale"; exit 1; }
echo "replay=200 stale=$stale"

echo "== 批阅 =="
review=$(curl -sS -b "$teacher" -H 'Content-Type: application/json' \
  -d "{\"version_id\":\"$v1\",\"decision\":\"pass\",\"feedback\":\"ok\"}" \
  "$BASE/api/assignments/$aid/reviews")
points=$(echo "$review" | json "['points']")
state=$(echo "$review" | json "['state']")
[ "$state" = "PASSED" ] || { echo "批阅后状态应为 PASSED，实际 $state"; exit 1; }
[ "$points" = "10" ] || { echo "首次通过应发 10 分，实际 $points"; exit 1; }
echo "state=$state points=$points"

echo "== 汇总/看板 =="
summary=$(curl -sS -b "$teacher" "$BASE/api/assignments/$aid/summary")
echo "$summary" | json "['total_points']" | grep -qx '10'
stats=$(curl -sS -b "$assistant" "$BASE/api/assignments/$aid/summary")
ro=$(echo "$stats" | json "['read_only_stats']")
[ "$ro" = "True" ] || { echo "助教汇总应为只读统计"; exit 1; }
board=$(curl -sS -o /dev/null -w '%{http_code}' -b "$assistant" "$BASE/api/assignments/$aid/board")
[ "$board" = "200" ] || { echo "助教看板应 200，实际 $board"; exit 1; }
echo "summary total=10 assistant_readonly=$ro board=$board"

echo "SMOKE OK"
