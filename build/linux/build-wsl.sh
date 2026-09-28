#!/usr/bin/env bash
# Build sessman for Linux (amd64) inside WSL / native Linux.
# Requires: Go, Node, Wails CLI, and GTK3 + WebKit2GTK 4.1 headers.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
echo "PWD=$(pwd)"

if [ -f /home/linuxbrew/.linuxbrew/bin/brew ]; then
  eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"
fi
export PATH="$(go env GOPATH)/bin:${HOME}/go/bin:${PATH}"

go version
node --version
npm --version

if ! command -v wails >/dev/null 2>&1; then
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
fi
wails version

if ! pkg-config --exists gtk+-3.0 webkit2gtk-4.1; then
  cat <<'EOF'
Missing Linux desktop build deps (GTK3 + WebKit2GTK 4.1).
Install once, then re-run this script:

  sudo apt-get update
  sudo apt-get install -y build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev

EOF
  exit 1
fi

pkg-config --modversion gtk+-3.0
pkg-config --modversion webkit2gtk-4.1

wails build -platform linux/amd64 -tags webkit2_41 -skipbindings
ls -la build/bin/
file build/bin/sessman || true
