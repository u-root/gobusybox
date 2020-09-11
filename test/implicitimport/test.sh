#!/bin/bash
set -eux

(cd ../../src/cmd/makebb && GO111MODULE=on go build .)
MAKEBB=../../src/cmd/makebb/makebb

for GO111MODULE in on auto;
do
  GO111MODULE=$GO111MODULE $MAKEBB -o bb-$GO111MODULE ./cmd/loghello
  test -f ./bb-$GO111MODULE

  HW=$(./bb-$GO111MODULE loghello 2>&1);
  test "$HW" == "Log Hello" || (echo "loghello not right" && exit 1)
done

# check reproducible
cmp bb-on bb-auto
rm bb-on bb-auto
