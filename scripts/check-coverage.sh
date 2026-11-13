#!/usr/bin/env bash
# 覆盖率门禁：整体 ≥85%，核心包（service/store/httpapi）各 ≥85%。
# 只统计 internal/*，cmd/server 是装配入口，不设门槛。
set -euo pipefail

threshold=85
core_packages=(
  "./internal/service"
  "./internal/store"
  "./internal/httpapi"
)

to_percent() {
  awk -v v="$1" 'BEGIN { printf "%d", (v + 0.0001) }'
}

fail=0

echo "== 全量覆盖率 =="
go test ./... -count=1 -coverprofile=coverage.out >/dev/null
total=$(go tool cover -func=coverage.out | awk '/^total:/ { sub("%", "", $3); print $3 }')
echo "total: ${total}% (门槛 ${threshold}%)"
if [ "$(to_percent "$total")" -lt "$threshold" ]; then
  echo "FAIL: 整体覆盖率低于门槛"
  fail=1
fi

for pkg in "${core_packages[@]}"; do
  name=$(basename "$pkg")
  out="coverage_${name}.out"
  go test "$pkg" -count=1 -coverprofile="$out" >/dev/null
  value=$(go tool cover -func="$out" | awk '/^total:/ { sub("%", "", $3); print $3 }')
  echo "${name}: ${value}% (门槛 ${threshold}%)"
  if [ "$(to_percent "$value")" -lt "$threshold" ]; then
    echo "FAIL: ${name} 覆盖率低于门槛"
    fail=1
  fi
done

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "覆盖率门禁通过"
