#!/bin/bash
set -eux

(cd ../../src/cmd/makebb && GO111MODULE=on go build .)
MAKEBB=../../src/cmd/makebb/makebb

# This uses the go.mod in src/
for GO111MODULE in on auto;
do
  GO111MODULE=$GO111MODULE $MAKEBB -o bb-$GO111MODULE ./cmd/dmesg ./cmd/strace ./nestedmod/cmd/p9ufs
  test -f ./bb-$GO111MODULE
done

# check reproducible
cmp bb-on bb-auto
rm bb-on bb-auto
