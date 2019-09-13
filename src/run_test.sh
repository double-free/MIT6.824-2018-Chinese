#!/bin/bash
set -e
if [ $# -ne 3 ]; then
	echo "Usage: $0 [labname] [test] [repeat time]"
	exit 1
fi
export "GOPATH=$(git rev-parse --show-toplevel)"
cd "${GOPATH}/src/$1"
for ((i=0;i<$3;i++))
do
	time go test -run $2
done
