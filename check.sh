#!/bin/sh
# Everything that has to pass before a commit. Usage: sh check.sh
# Needs a C compiler (for -race) and staticcheck:
#   go install honnef.co/go/tools/cmd/staticcheck@latest
set -eu
cd "$(dirname "$0")"
PATH="$PATH:$(go env GOPATH)/bin"
unformatted=$(gofmt -l cmd internal)
if [ -n "$unformatted" ]; then
  echo "gofmt needed on:" && echo "$unformatted" && exit 1
fi
go vet ./...
staticcheck ./...
(cd web && npm run build)
go test -race ./...
echo "All checks passed"
