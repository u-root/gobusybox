#!/bin/bash
set -eux

(cd ../../src/cmd/makebb && GO111MODULE=on go build .)
MAKEBB=../../src/cmd/makebb/makebb

for GO111MODULE in on auto;
do
  GO111MODULE=$GO111MODULE $MAKEBB ./cmd/loghello
  test -f ./bb || exit 1

  HW=$(./bb loghello 2>&1);
  test "$HW" == "Log Hello" || (echo "loghello not right" && exit 1)

  rm ./bb
done
