#!/usr/bin/env bash
# Builds the native release archives in dist/: huntsman-<os>-<arch>.tar.gz for macOS and Linux on arm64 and amd64,
# each with the Huntsman server, a pinned Lightpanda browser, the ad and tracker filter lists, and the huntsman
# command; plus huntsman-checksums.txt. Usage: scripts/package.sh [version] [os-arch ...]
set -euo pipefail

cd "$(dirname "$0")/.."
version=${1:-dev}
shift || true
targets=("$@")
[ ${#targets[@]} -gt 0 ] || targets=(darwin-arm64 darwin-amd64 linux-arm64 linux-amd64)

lightpanda_version=1.0.4
lightpanda_sha256() {
  case "$1" in
    lightpanda-aarch64-linux) echo f021d49a0b66d07bf2633f8e00870b9075293516f872695b0ca4aeb5848b6ea8 ;;
    lightpanda-aarch64-macos) echo 83b0ba5ae31086b675fb3ba50644bda075a13bebabe46dc5f7543952bc41ae46 ;;
    lightpanda-x86_64-linux) echo 7d22651129901203abdb95c64f49069c49e88f5211f4d8c4c6828c123c4adab4 ;;
    lightpanda-x86_64-macos) echo 8c02127c6fc2a713d166349bf1b511d470c8fcd684681927c978cc8bb5952d55 ;;
    *) return 1 ;;
  esac
}
filters=(
  https://easylist.to/easylist/easylist.txt
  https://easylist.to/easylist/easyprivacy.txt
  https://secure.fanboy.co.nz/fanboy-cookiemonster.txt
)

sha256() {
  if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi
}

tar_flags=()
[ "$(uname -s)" = Darwin ] && tar_flags=(--no-mac-metadata --no-xattrs)

dist=dist
cache=$dist/cache
rm -rf "$dist/stage" "$dist"/huntsman-*
mkdir -p "$cache/filters" "$dist/stage"

for url in "${filters[@]}"; do
  out=$cache/filters/$(basename "$url")
  curl -fsSL --retry 3 -o "$out" "$url"
  [ -s "$out" ] || { echo "empty filter list: $url" >&2; exit 1; }
done

for target in "${targets[@]}"; do
  os=${target%-*}
  arch=${target#*-}
  case "$arch" in arm64) lp_arch=aarch64 ;; amd64) lp_arch=x86_64 ;; *) echo "unknown arch: $arch" >&2; exit 1 ;; esac
  case "$os" in darwin) lp_os=macos ;; linux) lp_os=linux ;; *) echo "unknown os: $os" >&2; exit 1 ;; esac
  asset=lightpanda-$lp_arch-$lp_os
  lp=$cache/$lightpanda_version-$asset
  if [ ! -f "$lp" ]; then
    curl -fsSL --retry 3 -o "$lp.tmp" \
      "https://github.com/lightpanda-io/browser/releases/download/$lightpanda_version/$asset"
    mv "$lp.tmp" "$lp"
  fi
  echo "$(lightpanda_sha256 "$asset")  $lp" | sha256 -c - >/dev/null

  name=huntsman-$target
  stage=$dist/stage/$name
  mkdir -p "$stage/bin" "$stage/libexec" "$stage/filters"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
    -ldflags="-s -w -X main.version=$version" -o "$stage/libexec/huntsman" ./src/cmd/huntsman
  install -m 0755 "$lp" "$stage/libexec/lightpanda"
  install -m 0755 packaging/huntsman.sh "$stage/bin/huntsman"
  cp "$cache"/filters/*.txt "$stage/filters/"
  cp LICENSE NOTICE "$stage/"
  COPYFILE_DISABLE=1 tar "${tar_flags[@]}" -C "$dist/stage" -czf "$dist/$name.tar.gz" "$name"
  echo "built $dist/$name.tar.gz"
done

rm -rf "$dist/stage"
(cd "$dist" && sha256 huntsman-*.tar.gz >huntsman-checksums.txt)
