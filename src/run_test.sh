#!/bin/bash
if [ $# -ne 1 ]; then
	echo "Please specify a test to run"
	exit 1
fi
export "GOPATH=$(git rev-parse --show-toplevel)"
cd "${GOPATH}/src/raft"
go test -run $1
