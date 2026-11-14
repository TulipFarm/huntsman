#!/bin/sh
# Installs Huntsman, a local web reader for AI agents, into ~/.huntsman and starts it as a login service.
#
#   curl -fsSL https://huntsman.tulipfarm.ai/install.sh | sh
#
# Re-running it upgrades in place and keeps your settings and data. Options, as environment variables:
#   HUNTSMAN_VERSION=v0.3.0    install a specific release instead of the latest
#   HUNTSMAN_NO_SERVICE=1      install without starting the background service
#   HUNTSMAN_HOME=~/.huntsman  install somewhere else
# Uninstall with: huntsman uninstall
set -eu

repo=TulipFarm/huntsman
home=${HUNTSMAN_HOME:-$HOME/.huntsman}
bin_dir=${XDG_BIN_HOME:-$HOME/.local/bin}

say() { printf '%s\n' "$*"; }
die() {
  printf 'huntsman install: %s\n' "$*" >&2
  exit 1
}

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  MINGW* | MSYS* | CYGWIN*) die "Windows isn't supported natively. Run this inside WSL, or use Docker: https://huntsman.tulipfarm.ai/install" ;;
  *) die "unsupported OS $(uname -s). Use Docker instead: https://huntsman.tulipfarm.ai/install" ;;
esac
case "$(uname -m)" in
  arm64 | aarch64) arch=arm64 ;;
  x86_64 | amd64) arch=amd64 ;;
  *) die "unsupported CPU $(uname -m). Use Docker instead: https://huntsman.tulipfarm.ai/install" ;;
esac
# A shell running under Rosetta reports x86_64 on Apple silicon; prefer the native build.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n hw.optional.arm64 2>/dev/null || echo 0)" = 1 ]; then
  arch=arm64
fi
command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

if [ -n "${HUNTSMAN_RELEASE_URL:-}" ]; then
  base=$HUNTSMAN_RELEASE_URL
elif [ -n "${HUNTSMAN_VERSION:-}" ]; then
  base=https://github.com/$repo/releases/download/$HUNTSMAN_VERSION
else
  base=https://github.com/$repo/releases/latest/download
fi
name=huntsman-$os-$arch

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading $name..."
curl -fsSL --retry 3 -o "$tmp/$name.tar.gz" "$base/$name.tar.gz" || die "download failed: $base/$name.tar.gz"
curl -fsSL --retry 3 -o "$tmp/checksums.txt" "$base/huntsman-checksums.txt" || die "download failed: $base/huntsman-checksums.txt"
want=$(awk -v f="$name.tar.gz" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || die "no checksum for $name.tar.gz"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$name.tar.gz" | awk '{ print $1 }')
else
  got=$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{ print $1 }')
fi
[ "$got" = "$want" ] || die "checksum mismatch for $name.tar.gz"
tar -xzf "$tmp/$name.tar.gz" -C "$tmp"

if [ "$os" = linux ] && "$tmp/$name/libexec/lightpanda" version 2>&1 | grep -q "GLIBC"; then
  die "this Linux is too old for the bundled browser (it needs a newer glibc). Use Docker instead: https://huntsman.tulipfarm.ai/install"
fi
if [ "$os" = darwin ]; then
  xattr -dr com.apple.quarantine "$tmp/$name" 2>/dev/null || true
fi

was_running=0
if [ -x "$home/bin/huntsman" ] && "$home/bin/huntsman" status >/dev/null 2>&1; then
  was_running=1
  "$home/bin/huntsman" stop >/dev/null
fi

# Replace the program files; keep data/, env, and the log.
mkdir -p "$home/data"
rm -rf "${home:?}/bin" "${home:?}/libexec" "${home:?}/filters"
for d in bin libexec filters; do
  mv "$tmp/$name/$d" "$home/$d"
done
cp "$tmp/$name/LICENSE" "$tmp/$name/NOTICE" "$home/"
if [ ! -f "$home/env" ]; then
  cat >"$home/env" <<'ENV'
# Huntsman settings, read by `huntsman start` and `huntsman run`. Run `huntsman restart` after editing.
# Every setting: https://huntsman.tulipfarm.ai/install#configuration
HUNTSMAN_ADDR=127.0.0.1:8080
HUNTSMAN_WORKERS=8
# Requests from other machines need a token. Only set it if you change HUNTSMAN_ADDR to listen beyond this machine.
# HUNTSMAN_TOKEN=
ENV
fi

mkdir -p "$bin_dir"
ln -sf "$home/bin/huntsman" "$bin_dir/huntsman"

say "Installed Huntsman $("$home/libexec/huntsman" version) in $home"

if [ "${HUNTSMAN_NO_SERVICE:-0}" = 1 ] && [ $was_running = 0 ]; then
  say "Start it with: huntsman start"
else
  # Something else on the port would make the service fail to start; say so instead.
  addr=$(sed -n 's/^HUNTSMAN_ADDR=//p' "$home/env" | tail -n 1)
  port=${addr##*:}
  if curl -s -m 2 -o /dev/null "http://127.0.0.1:${port:-8080}/" 2>/dev/null; then
    die "port ${port:-8080} is already in use. Set HUNTSMAN_ADDR in $home/env to a free port, then run: huntsman start"
  fi
  "$home/bin/huntsman" start
fi

case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) say "Add $bin_dir to your PATH to use the huntsman command: export PATH=\"$bin_dir:\$PATH\"" ;;
esac
say "Next, connect your agents: https://huntsman.tulipfarm.ai/agents"
