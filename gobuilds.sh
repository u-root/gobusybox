#!/bin/bash
set -eux

# This one hasn't been migrated to the go test yet.
(cd ./test/requestconflict && ./test.sh)

(cd ./src/cmd/makebb && GO111MODULE=on go build .)
(cd ./test && go test --makebb=../src/cmd/makebb/makebb -v)
