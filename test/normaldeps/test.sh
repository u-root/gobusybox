#!/bin/bash
set -eux

(cd ../../src/cmd/makebb && GO111MODULE=on go build .)
MAKEBB=../../src/cmd/makebb/makebb

for GO111MODULE in on auto;
do
  GO111MODULE=$GO111MODULE $MAKEBB ./mod1/cmd/*
  test -f ./bb || exit 1

  BPID=$(echo $$);
  GETPPID=$(./bb getppid);
  test "$BPID" == "$GETPPID" || (echo "PIDs not the same" && exit 1)

  HW=$(./bb helloworld);
  test "$HW" == "test/normaldeps/mod2/hello: test/normaldeps/mod2/v2/hello" || (echo "hello world not right" && exit 1)

  rm ./bb
done
