#!/bin/bash

set -e

if [ "$#" -ne 2 ]; then
	echo "Usage $(basename $0) <base git revision> <head git revision>"
fi

BASE_NAME="BASE"
NEW_NAME="HEAD"
BASE_REV=$(git rev-parse $BASE_NAME)
NEW_REV=$(git rev-parse $NEW_NAME)

create_exec () {
	REV=$1
	git checkout $REV
	go build -o $REV ./cmd/chisel
}
create_exec $NEW_REV
create_exec $BASE_REV

hyperfine "./$BASE_REV info --release ../chisel-releases 'python3.12_core'" -n "$BASE_NAME" "./$NEW_REV info --release ../chisel-releases 'python3.12_core'" -n "$NEW_NAME"
