#!/usr/bin/env bash
# Seeds a fresh database, starts Eimsky on :8090 and runs the end-to-end flow.
# Needs Postgres (DATABASE_URL) and Node with Playwright (`npm ci` in e2e/).
set -euo pipefail
cd "$(dirname "$0")/.."
export DATABASE_URL="${E2E_DATABASE_URL:-${DATABASE_URL:-postgres://eimsky:eimsky@localhost:5432/eimsky_test?sslmode=disable}}"
export DATA_DIR="$(mktemp -d)"
export ADDR=":8090" BASE_URL="http://localhost:8090" DEV_MODE=true JOBS_INTERVAL=2s PUSH_ENABLED=false
go build -o bin/eimsky ./cmd/eimsky
./bin/eimsky seed --reset
./bin/eimsky serve > "$DATA_DIR/server.log" 2>&1 &
PID=$!
trap 'kill $PID 2>/dev/null || true' EXIT
for i in $(seq 1 50); do curl -sf localhost:8090/healthz >/dev/null && break; sleep 0.2; done
BASE_URL=http://localhost:8090 node e2e/flow.js || { echo "--- server log ---"; tail -40 "$DATA_DIR/server.log"; exit 1; }
