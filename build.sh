#!/bin/sh
# Builds the page into internal/webui/dist, then the rehearse binary that
# embeds it. Usage: sh build.sh
set -eu
cd "$(dirname "$0")"
(cd web && npm ci --no-fund --no-audit && npm run build)
go build -o rehearse ./cmd/rehearse
echo "Built ./rehearse"
