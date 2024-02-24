#!/bin/bash
set -eux

(cd ./src/cmd/makebb && GO111MODULE=on go build -covermode=atomic .)
(cd ./test && go test --makebb=../src/cmd/makebb/makebb -v)
