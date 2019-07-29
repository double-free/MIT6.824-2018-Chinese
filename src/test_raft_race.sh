#!/bin/bash
export "GOPATH=$(git rev-parse --show-toplevel)"
cd "${GOPATH}/src/raft"
go test -race
