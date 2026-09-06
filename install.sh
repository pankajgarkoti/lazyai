#!/bin/sh
# Install LazyAI from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/pankajgarkoti/lazyai/main/install.sh | sh
#
# Environment:
#   LAZYAI_VERSION      tag to install (default: latest release), e.g. v0.2.0
#   LAZYAI_INSTALL_DIR  destination directory (default: ~/.local/bin)
#
# The archive's SHA-256 is verified against the release's SHA256SUMS before
# anything is installed. Requires curl, tar and shasum or sha256sum.
set -eu

repo="pankajgarkoti/lazyai"
version="${LAZYAI_VERSION:-}"
dest="${LAZYAI_INSTALL_DIR:-$HOME/.local/bin}"

fail() { printf 'lazyai install: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "$1 is required"; }
need curl
need tar

# Bounded, retried downloads so a stalled CDN connection fails instead of hanging.
fetch() { curl -fsSL --connect-timeout 15 --max-time 120 --retry 3 --retry-delay 2 "$@"; }

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "unsupported OS $(uname -s); build from source with: go install ./cmd/lazyai" ;;
esac
case "$(uname -m)" in
  arm64 | aarch64) arch=arm64 ;;
  x86_64 | amd64) arch=amd64 ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac

if [ -z "$version" ]; then
  version="$(fetch "https://api.github.com/repos/$repo/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
  [ -n "$version" ] || fail "could not determine the latest release"
fi
case "$version" in v*) ;; *) version="v$version" ;; esac

name="lazyai_${version}_${os}_${arch}"
base="https://github.com/$repo/releases/download/$version"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

printf 'lazyai install: downloading %s\n' "$name.tar.gz"
fetch -o "$tmp/$name.tar.gz" "$base/$name.tar.gz" || fail "no archive for $version/$os/$arch"
fetch -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || fail "release $version has no SHA256SUMS"

want="$(grep " $name.tar.gz\$" "$tmp/SHA256SUMS" | awk '{print $1}')"
[ -n "$want" ] || fail "SHA256SUMS has no entry for $name.tar.gz"
if command -v sha256sum >/dev/null 2>&1; then
  have="$(sha256sum "$tmp/$name.tar.gz" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  have="$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{print $1}')"
else
  fail "sha256sum or shasum is required to verify the download"
fi
[ "$have" = "$want" ] || fail "checksum mismatch for $name.tar.gz (have $have, want $want)"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
[ -f "$tmp/$name/lazyai" ] || fail "archive did not contain lazyai"
mkdir -p "$dest"
install -m 0755 "$tmp/$name/lazyai" "$dest/lazyai"

printf 'lazyai install: installed %s to %s\n' "$("$dest/lazyai" --version)" "$dest/lazyai"
case ":$PATH:" in
  *":$dest:"*) ;;
  *) printf 'lazyai install: add %s to your PATH\n' "$dest" ;;
esac
printf 'lazyai install: requires opencode on PATH; run lazyai inside a project to start\n'
