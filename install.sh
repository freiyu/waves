#!/bin/sh
# Installs the waves binary for your CPU into a directory on your PATH.
#
#   sh install.sh                      latest release into ~/.local/bin
#   sh install.sh --version 1.0.0      a specific release
#   sh install.sh --dir /usr/local/bin where to put it
#   sh install.sh --from ./release     use files you already downloaded (waves-linux-<arch> and SHA256SUMS)
#
# It downloads over HTTPS from GitHub Releases, checks the file against SHA256SUMS and installs only if it matches.
# SHA256SUMS comes from the same place as the binary, so it catches a damaged download, not a compromised release.
# Nothing else is changed: no sudo, no service, no shell profile.
set -eu

repo="freiyu/waves"
version="latest"
dir="${HOME}/.local/bin"
from=""

die() { echo "install: $*" >&2; exit 1; }
usage() {
  cat <<'USAGE'
Installs the waves binary for your CPU.

  sh install.sh                       latest release into ~/.local/bin
  sh install.sh --version 1.0.0       a specific release
  sh install.sh --dir /usr/local/bin  where to put it
  sh install.sh --from ./release      use files you already downloaded (waves-linux-<arch> and SHA256SUMS)

The download comes over HTTPS from GitHub Releases and is checked against SHA256SUMS before anything is installed.
SHA256SUMS comes from the same release, so this catches a damaged download, not a compromised release.
Nothing else changes: no sudo, no service, no shell profile.
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --version) [ $# -ge 2 ] || die "--version needs a value"; version="$2"; shift 2 ;;
    --dir)     [ $# -ge 2 ] || die "--dir needs a value"; dir="$2"; shift 2 ;;
    --from)    [ $# -ge 2 ] || die "--from needs a value"; from="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option $1 (try --help)" ;;
  esac
done

case "$version" in
  latest) ;;
  *[!0-9A-Za-z._-]*|"") die "--version takes a release number such as 1.0.0" ;;
esac
[ "$(uname -s)" = "Linux" ] || die "Waves runs on Linux and WSL2 only"
case "$(uname -m)" in
  x86_64|amd64)  arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) die "unsupported CPU $(uname -m): releases are built for amd64 and arm64" ;;
esac
command -v sha256sum >/dev/null 2>&1 || die "sha256sum is required"

name="waves-linux-${arch}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' INT TERM

fetch() { # fetch <file name>
  if [ -n "$from" ]; then
    [ -f "$from/$1" ] || die "$from/$1 not found: the folder must hold waves-linux-$arch and SHA256SUMS"
    cp "$from/$1" "$tmp/$1"
    return
  fi
  if [ "$version" = "latest" ]; then base="https://github.com/${repo}/releases/latest/download"
  else base="https://github.com/${repo}/releases/download/v${version#v}"; fi
  if command -v curl >/dev/null 2>&1; then
    curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL "$base/$1" -o "$tmp/$1" || die "could not download $base/$1 (check the network and, with --version, that the release exists)"
  elif command -v wget >/dev/null 2>&1; then
    wget --https-only -q "$base/$1" -O "$tmp/$1" || die "could not download $base/$1 (check the network and, with --version, that the release exists)"
  else
    die "curl or wget is required"
  fi
}

fetch "$name"
fetch SHA256SUMS
want="$(grep "  ${name}\$" "$tmp/SHA256SUMS" || true)"
[ -n "$want" ] || die "SHA256SUMS has no entry for $name"
(cd "$tmp" && echo "$want" | sha256sum -c - >/dev/null 2>&1) || die "checksum mismatch for $name: nothing was installed"

mkdir -p "$dir"
install -m 755 "$tmp/$name" "$dir/.waves.$$" && mv -f "$dir/.waves.$$" "$dir/waves" || { rm -f "$dir/.waves.$$"; die "could not write $dir/waves"; }
got="$("$dir/waves" version)" || die "installed $dir/waves but it does not run on this machine"
echo "Installed $got to $dir/waves"
case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "Note: $dir is not on your PATH. Add it, for example: export PATH=\"$dir:\$PATH\"" ;;
esac
echo "Next: waves setup"
