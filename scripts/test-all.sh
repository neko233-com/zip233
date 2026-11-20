#!/bin/sh
# zip233 全量回归：依赖整洁性、编译、静态检查、单元/竞态测试、CLI smoke 与图标幂等。
# POSIX sh，可在 Linux/macOS 与 Windows(Git Bash) 上运行。
set -eu

cd "$(dirname "$0")/.."

step() { printf '\n==> %s\n' "$1"; }

step 'go.mod / go.sum 整洁性'
if go help mod tidy 2>/dev/null | grep -q -- '-diff'; then
  go mod tidy -diff
else
  go mod tidy
  git diff --exit-code -- go.mod go.sum
fi

step '编译'
go build ./...

step '静态检查'
go vet ./...

step '单元测试'
go test -count=1 ./...

step '竞态检测'
if [ "$(go env CGO_ENABLED)" = "1" ] &&
  { command -v gcc >/dev/null 2>&1 || command -v clang >/dev/null 2>&1 || command -v cc >/dev/null 2>&1; }; then
  go test -race ./...
else
  printf '  CGO 或 C 编译器不可用，跳过 -race\n'
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' 0 1 2 3 15

cli() { go run ./cmd/zip233-cli "$@"; }

step 'CLI smoke: formats --json'
formats=$(cli formats --json)
printf '%s\n' "$formats" | grep -q '"create"'
printf '%s\n' "$formats" | grep -q '"read"'

step 'CLI smoke: bench --json'
bench=$(cli bench --formats zip --json)
printf '%s\n' "$bench" | grep -q '"rows"'
printf '%s\n' "$bench" | grep -q '"ok": *true'

step 'CLI smoke: split / join 往返'
printf 'zip233 test-all.sh round trip payload\n' > "$tmp/source.bin"
split_out=$(cli split "$tmp/source.bin" --size 16 --json)
printf '%s\n' "$split_out" | grep -q '"parts"'
join_out=$(cli join "$tmp/source.bin.part-001" --output "$tmp/joined.bin" --json)
printf '%s\n' "$join_out" | grep -q '"size"'
cmp "$tmp/source.bin" "$tmp/joined.bin"

step '图标生成幂等性'
go run ./cmd/zip233-icons -out "$tmp/icons-a" >/dev/null
go run ./cmd/zip233-icons -out "$tmp/icons-b" >/dev/null
diff -r "$tmp/icons-a" "$tmp/icons-b"

printf '\n全部检查通过。\n'
