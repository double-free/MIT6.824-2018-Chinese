#!/bin/bash
set -e
if [ $# -ne 1 ]; then
    echo "Usage: $0 [labname]"
    exit 1
fi
export "GOPATH=$(git rev-parse --show-toplevel)"
cd "${GOPATH}/src/$1"
go test -race
