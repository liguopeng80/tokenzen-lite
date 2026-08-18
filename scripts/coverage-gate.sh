#!/usr/bin/env bash
# 接口层测试覆盖率下限门禁。
#
# 只守 server/internal/api 一个包：它是全部对外行为的汇聚处，
# 覆盖率掉下来意味着新增端点没有配套用例，而端点缺用例的后果是
# 权限边界与响应契约的回归无人发现。
#
# 下限起步 70%，随迭代逐步提到 80%。
set -euo pipefail

THRESHOLD="${COVERAGE_THRESHOLD:-70}"
PACKAGE="${COVERAGE_PACKAGE:-./internal/api/}"
PROFILE="$(mktemp -t tzl-coverage-XXXXXX.out)"
trap 'rm -f "$PROFILE"' EXIT

cd "$(dirname "$0")/../server"

if [ -z "${TZL_TEST_DATABASE_URL:-}" ]; then
    echo "覆盖率门禁已跳过：未设置 TZL_TEST_DATABASE_URL，接口层集成测试不会执行" >&2
    exit 0
fi

go test -p 1 -coverprofile="$PROFILE" "$PACKAGE" >/dev/null
ACTUAL="$(go tool cover -func="$PROFILE" | awk '/^total:/ {print $3}' | tr -d '%')"

# 用整数比较，避免依赖 bc 之类的额外命令
if awk -v a="$ACTUAL" -v t="$THRESHOLD" 'BEGIN { exit !(a < t) }'; then
    echo "接口层语句覆盖率 ${ACTUAL}%，低于下限 ${THRESHOLD}%。" >&2
    echo "请为新增或改动的端点补充测试；未覆盖的函数清单：" >&2
    go tool cover -func="$PROFILE" | awk '$3 == "0.0%"' >&2
    exit 1
fi

echo "接口层语句覆盖率 ${ACTUAL}%（下限 ${THRESHOLD}%）"
