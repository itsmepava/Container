#!/usr/bin/env bash
# Start Eimsky for local testing on Linux or macOS. No sudo, no system database:
# it downloads Go into .tools/ if needed and runs a private PostgreSQL in ./data.
#
#   ./start.sh              start (demo data is loaded on the first run)
#   ./start.sh --reset      start fresh with new demo data
#   ./start.sh --port 9000  use another port
set -euo pipefail
cd "$(dirname "$0")"

GO_VERSION=1.26.0
need_go() {
  command -v go >/dev/null 2>&1 || return 0
  local v; v=$(go env GOVERSION 2>/dev/null | sed 's/^go//')
  local major minor; major=${v%%.*}; minor=$(echo "$v" | cut -d. -f2)
  [ "${major:-0}" -lt 1 ] || { [ "$major" -eq 1 ] && [ "${minor:-0}" -lt 24 ]; }
}

if need_go; then
  if [ ! -x .tools/go/bin/go ]; then
    case "$(uname -s)" in Linux) os=linux ;; Darwin) os=darwin ;; *) echo "Unsupported system: $(uname -s)"; exit 1 ;; esac
    case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo "Unsupported CPU: $(uname -m)"; exit 1 ;; esac
    echo "Downloading Go $GO_VERSION (one time, about 70 MB)…"
    mkdir -p .tools
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.${os}-${arch}.tar.gz" | tar -xz -C .tools
  fi
  export PATH="$PWD/.tools/go/bin:$PATH"
fi

echo "Building Eimsky…"
go build -o bin/eimsky ./cmd/eimsky
exec ./bin/eimsky demo "$@"
