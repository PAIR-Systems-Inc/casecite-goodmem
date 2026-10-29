#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
npm --prefix frontend ci --ignore-scripts
npm --prefix frontend run build
npm --prefix word-addin ci --ignore-scripts
npm --prefix word-addin run build -- --base /word-addin/
# This directory contains only generated, embedded web assets.
find server/web/assets -mindepth 1 ! -name .keep -delete
cp -R frontend/dist/. server/web/assets/
mkdir -p server/web/assets/word-addin
cp -R word-addin/dist/. server/web/assets/word-addin/
cp word-addin/manifest.xml server/web/assets/word-addin/manifest.template.xml
cp NOTICE.md server/web/assets/third-party-notices.txt
mkdir -p bin
cd server
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o ../bin/casecite ./cmd/casecite
