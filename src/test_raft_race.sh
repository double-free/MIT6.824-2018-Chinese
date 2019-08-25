#!/bin/bash
export "GOPATH=$(git rev-parse --show-toplevel)"
cd "${GOPATH}/src/kvraft"
go test -race
