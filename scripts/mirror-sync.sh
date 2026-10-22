#!/usr/bin/env bash
# 把 gurl 的最新 GitHub Release 同步到自建镜像，供国内用户下载与自动更新回落。
#
# 用法（cron 例：每 30 分钟一次）：
#   */30 * * * * MIRROR_BASE=http://releases.example.com:8816 /path/to/mirror-sync.sh
#
# 环境变量：
#   MIRROR_BASE   必填。镜像对外地址（含端口，不含末尾斜杠），写进 latest JSON 的下载链接。
#   SERVE_DIR     文件服务根目录，默认 /srv/gurl-releases。同步后目录形如：
#                   $SERVE_DIR/files/gurl_v0.4.0_darwin_arm64.tar.gz
#                   $SERVE_DIR/repos/MACDfree/gurl/releases/latest   ← GitHub 形状的 JSON
#   GH_PROXY      可选。GitHub 下载代理前缀（如 https://ghfast.top/），服务器直连 GitHub 不畅时用。
#
# 依赖：curl、jq。
set -euo pipefail

: "${MIRROR_BASE:?需要设置 MIRROR_BASE，例如 http://releases.example.com:8816}"
SERVE_DIR=${SERVE_DIR:-/srv/gurl-releases}
GH_PROXY=${GH_PROXY:-}
REPO=MACDfree/gurl

FILES=$SERVE_DIR/files
REPOS=$SERVE_DIR/repos/$REPO/releases
mkdir -p "$FILES" "$REPOS"

meta=$(curl -fsSL --max-time 30 "https://api.github.com/repos/$REPO/releases/latest")
tag=$(jq -r .tag_name <<<"$meta")
[ -n "$tag" ] && [ "$tag" != null ] || { echo "拿不到 tag，跳过" >&2; exit 1; }

# 已是最新则什么都不做：latest 文件的存在即状态，不另设标记文件
if [ -f "$REPOS/latest" ] && [ "$(jq -r .tag_name "$REPOS/latest" 2>/dev/null)" = "$tag" ]; then
  exit 0
fi

echo "同步 $tag"
tmp=$(mktemp "$REPOS/.latest.XXXXXX")
trap 'rm -f "$tmp"' EXIT

# 逐个资产：先落盘再拼 JSON，下载中途失败不会污染 latest
assets_json="[]"
while read -r name url; do
  dest="$FILES/$name"
  if [ ! -s "$dest" ]; then
    echo "  下载 $name"
    curl -fL --retry 3 --max-time 600 -o "$dest.part" "${GH_PROXY}${url}"
    mv "$dest.part" "$dest"
  fi
  assets_json=$(jq -c --arg n "$name" --arg u "$MIRROR_BASE/files/$name" \
    '. + [{name: $n, browser_download_url: $u}]' <<<"$assets_json")
done < <(jq -r '.assets[] | [.name, .browser_download_url] | @tsv' <<<"$meta")

jq -c --arg tag "$tag" --arg url "https://github.com/$REPO/releases/tag/$tag" \
  --argjson assets "$assets_json" \
  '{tag_name: $tag, html_url: $url, assets: $assets}' <<<"{}" >"$tmp"

mv "$tmp" "$REPOS/latest" # 原子替换，客户端要么看到旧的完整版要么看到新的完整版
trap - EXIT
echo "完成：$MIRROR_BASE/repos/$REPO/releases/latest -> $tag"
