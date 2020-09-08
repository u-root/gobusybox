#!/bin/bash
set -eux

(cd ../../src/cmd/makebb && GO111MODULE=on go build .)
MAKEBB=../../src/cmd/makebb/makebb

# This uses the go.mod in src/
for GO111MODULE in on auto;
do
  GO111MODULE=$GO111MODULE $MAKEBB ./cmd/dmesg ./cmd/strace
  test -f ./bb || exit 1
  rm ./bb

  # nested modules
  GO111MODULE=$GO111MODULE $MAKEBB ./cmd/dmesg ./cmd/strace ./nestedmod/cmd/p9ufs
  test -f ./bb || exit 1
  rm ./bb
done
