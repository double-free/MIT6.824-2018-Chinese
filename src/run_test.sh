#!/bin/bash
set -e
if [ $# -ne 2 ]; then
	echo "Usage: $0 [test] [repeat time]"
	exit 1
fi
export "GOPATH=$(git rev-parse --show-toplevel)"
cd "${GOPATH}/src/raft"
for ((i=0;i<$2;i++))
do
	time go test -run $1
done
