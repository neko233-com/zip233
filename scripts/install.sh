#!/usr/bin/env bash
# zip233 macOS 一键安装 / 更新 / 卸载脚本。
# 用法: ./install.sh [--version v0.2.1] [--uninstall] [--no-shell-integration] [--silent] [--mirror URL] [--force]
set -euo pipefail

VERSION="latest"
UNINSTALL=0
NO_SHELL=0
SILENT=0
MIRROR=""
FORCE=0

usage() {
  cat <<'EOF'
用法: install.sh [选项]
  --version, -v <版本>      指定版本（默认 latest）
  --uninstall               卸载 zip233
  --no-shell-integration    不集成右键菜单（macOS 无此集成，自动跳过）
  --silent, -s              静默模式，只输出错误
  --mirror <URL>            镜像基础地址，失败自动回退官方源
  --force                   强制重新下载并覆盖已安装的应用
  --help, -h                显示本帮助
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version|-v) VERSION="${2:-}"; shift 2 ;;
    --uninstall) UNINSTALL=1; shift ;;
    --no-shell-integration) NO_SHELL=1; shift ;;
    --silent|-s) SILENT=1; shift ;;
    --mirror) MIRROR="${2:-}"; shift 2 ;;
    --force) FORCE=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) echo "▲ zip233 错误: 未知参数 $1" >&2; usage; exit 1 ;;
  esac
done

os="$(uname -s)"
if [ "$os" != "Darwin" ]; then
  echo "▲ zip233 错误: zip233 桌面版暂不支持 $os，仅支持 macOS 与 Windows，已退出。" >&2
  exit 1
fi

log()  { if [ "$SILENT" -eq 0 ]; then echo "▲ zip233 $*"; fi; }
warn() { echo "▲ zip233 警告: $*" >&2; }
err()  { echo "▲ zip233 错误: $*" >&2; }
die()  { err "$*"; exit 1; }

command -v curl >/dev/null 2>&1 || die "缺少 curl，请先安装命令行工具"
command -v shasum >/dev/null 2>&1 || die "缺少 shasum，请先安装命令行工具"

# GitHub Release 的 JSON 解析：优先 jq，否则 python3。
json_query() {
  local file="$1" kind="$2"
  shift 2
  if command -v jq >/dev/null 2>&1; then
    case "$kind" in
      count) jq -r '.assets | length' "$file" ;;
      top)   jq -r ".$1" "$file" ;;
      asset) jq -r ".assets[$1].$2" "$file" ;;
    esac
  elif command -v python3 >/dev/null 2>&1; then
    python3 - "$file" "$kind" "$@" <<'PY'
import json, sys
file, kind = sys.argv[1], sys.argv[2]
args = sys.argv[3:]
with open(file, encoding="utf-8") as fh:
    data = json.load(fh)
if kind == "count":
    value = len(data.get("assets") or [])
elif kind == "top":
    value = data.get(args[0]) if isinstance(data, dict) else None
else:
    index, key = int(args[0]), args[1]
    assets = data.get("assets") or []
    value = assets[index].get(key) if index < len(assets) else None
if value is None:
    print("")
elif isinstance(value, bool):
    print("true" if value else "false")
else:
    print(value)
PY
  else
    die "需要 jq 或 python3 来解析 GitHub Release API 响应，请先安装其中一个"
  fi
}

# ---------------------------------------------------------------- uninstall
if [ "$UNINSTALL" -eq 1 ]; then
  log "正在卸载 zip233 ..."
  removed=0
  for path in "/Applications/zip233.app" "$HOME/Applications/zip233.app"; do
    if [ -e "$path" ]; then
      rm -rf "$path" || die "删除 $path 失败"
      log "已删除: $path"
      removed=1
    fi
  done
  for path in \
    "$HOME/Library/Application Support/zip233" \
    "$HOME/Library/Preferences/com.neko233.zip233.plist" \
    "$HOME/Library/Caches/com.neko233.zip233" \
    "$HOME/Library/Logs/zip233"; do
    if [ -e "$path" ]; then
      rm -rf "$path" || true
      log "已清理本地配置: $path"
    fi
  done
  if [ "$removed" -eq 0 ]; then warn "未找到已安装的 zip233.app"; fi
  log "卸载完成"
  exit 0
fi

# ---------------------------------------------------------------- metadata
api="https://api.github.com/repos/neko233-com/zip233/releases"
if [ -z "$VERSION" ] || [ "$VERSION" = "latest" ]; then
  api_url="$api/latest"
else
  tag="$VERSION"
  case "$tag" in v*) ;; *) tag="v$tag" ;; esac
  api_url="$api/tags/$tag"
fi

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/zip233-install.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT

log "正在查询 Release 信息: $api_url"
curl -fsSL --retry 3 --connect-timeout 20 \
  -H "User-Agent: zip233-installer" \
  -H "Accept: application/vnd.github+json" \
  "$api_url" -o "$tmpdir/release.json" || die "获取 Release 信息失败"

tag="$(json_query "$tmpdir/release.json" top tag_name)"
if [ -z "$tag" ] || [ "$tag" = "null" ]; then die "Release 信息缺少 tag_name"; fi
version="${tag#v}"
asset_name="zip233-$version.dmg"
log "目标版本: $tag"

count="$(json_query "$tmpdir/release.json" count)"
[ "$count" -gt 0 ] 2>/dev/null || die "Release $tag 中没有可下载的资产"

idx=""
i=0
while [ "$i" -lt "$count" ]; do
  name="$(json_query "$tmpdir/release.json" asset "$i" name)"
  if [ "$name" = "$asset_name" ]; then idx="$i"; break; fi
  i=$((i + 1))
done
if [ -z "$idx" ]; then
  i=0
  while [ "$i" -lt "$count" ]; do
    name="$(json_query "$tmpdir/release.json" asset "$i" name)"
    case "$name" in *.dmg) idx="$i"; asset_name="$name"; break ;; esac
    i=$((i + 1))
  done
fi
[ -n "$idx" ] || die "Release $tag 中找不到 DMG 安装包"
log "选择安装包: $asset_name"

digest_raw="$(json_query "$tmpdir/release.json" asset "$idx" digest)"
case "$digest_raw" in
  sha256:*) digest="${digest_raw#sha256:}" ;;
  null|"")  digest="" ;;
  *)        digest="$digest_raw" ;;
esac
if [ -z "$digest" ]; then
  digest_name="$asset_name.sha256"
  i=0
  while [ "$i" -lt "$count" ]; do
    name="$(json_query "$tmpdir/release.json" asset "$i" name)"
    if [ "$name" = "$digest_name" ]; then
      url="$(json_query "$tmpdir/release.json" asset "$i" browser_download_url)"
      curl -fsSL --retry 3 "$url" -o "$tmpdir/digest.txt" || die "下载摘要文件失败"
      digest="$(grep -Eo '[0-9a-fA-F]{64}' "$tmpdir/digest.txt" | head -n 1 | tr 'A-Z' 'a-z')"
      break
    fi
    i=$((i + 1))
  done
fi
[ -n "$digest" ] || die "无法获取 $asset_name 的 SHA-256 摘要，出于安全考虑已中止"

official_url="$(json_query "$tmpdir/release.json" asset "$idx" browser_download_url)"
destination="$tmpdir/$asset_name"

download() {
  curl -fL --retry 3 --connect-timeout 20 -o "$1" "$2"
}

ok=0
if [ -n "$MIRROR" ]; then
  mirror_url="${MIRROR%/}/$tag/$asset_name"
  log "尝试镜像源: $mirror_url"
  if download "$destination" "$mirror_url"; then
    ok=1
  else
    warn "镜像下载失败，回退官方源"
    rm -f "$destination"
  fi
fi
if [ "$ok" -eq 0 ]; then
  log "从官方源下载: $official_url"
  download "$destination" "$official_url" || die "下载失败: $official_url"
fi

actual="$(shasum -a 256 "$destination" | awk '{print $1}')"
if [ "$actual" != "$digest" ]; then
  rm -f "$destination"
  die "SHA-256 校验失败：期望 $digest，实际 $actual，已中止安装"
fi
log "SHA-256 校验通过"

# ---------------------------------------------------------------- DMG install
command -v hdiutil >/dev/null 2>&1 || die "缺少 hdiutil，请在 macOS 主机上运行本脚本"
mount_point="$tmpdir/mount"
mkdir -p "$mount_point"
log "正在挂载 DMG"
hdiutil attach "$destination" -nobrowse -mountpoint "$mount_point" >/dev/null || die "挂载 DMG 失败"

app_src=""
for candidate in "$mount_point"/*.app; do
  if [ -d "$candidate" ]; then app_src="$candidate"; break; fi
done
if [ -z "$app_src" ]; then
  hdiutil detach "$mount_point" >/dev/null 2>&1 || true
  die "DMG 中找不到 .app 应用包"
fi

if [ -w "/Applications" ]; then
  install_dir="/Applications"
else
  install_dir="$HOME/Applications"
  mkdir -p "$install_dir"
fi
app_dst="$install_dir/zip233.app"

log "安装到: $app_dst"
if [ -e "$app_dst" ] && [ "$FORCE" -eq 0 ]; then log "覆盖已存在的安装"; fi
rm -rf "$app_dst"
cp -R "$app_src" "$app_dst" || die "复制应用失败"
hdiutil detach "$mount_point" >/dev/null 2>&1 || true

if [ "$NO_SHELL" -eq 1 ]; then
  log "macOS 不提供 Windows 资源管理器右键菜单集成，此项已跳过。"
fi

log "安装完成。"
