#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
npm --prefix frontend ci --ignore-scripts
npm --prefix frontend run build
# This directory contains only generated, embedded web assets.
find server/web/assets -mindepth 1 ! -name .keep -delete
cp -R frontend/dist/. server/web/assets/
cp NOTICE.md server/web/assets/third-party-notices.txt
mkdir -p bin
cd server
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o ../bin/casecite ./cmd/casecite
