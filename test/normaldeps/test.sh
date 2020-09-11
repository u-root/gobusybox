#!/bin/bash
set -eux

(cd ../../src/cmd/makebb && GO111MODULE=on go build .)
MAKEBB=../../src/cmd/makebb/makebb

for GO111MODULE in on auto;
do
  GO111MODULE=$GO111MODULE $MAKEBB -o bb-$GO111MODULE ./mod1/cmd/*
  test -f ./bb-$GO111MODULE

  BPID=$(echo $$);
  GETPPID=$(./bb-$GO111MODULE getppid);
  test "$BPID" == "$GETPPID" || (echo "PIDs not the same" && exit 1)

  HW=$(./bb-$GO111MODULE helloworld);
  test "$HW" == "test/normaldeps/mod2/hello: test/normaldeps/mod2/v2/hello" || (echo "hello world not right" && exit 1)
done

# check reproducible
cmp bb-on bb-auto
rm bb-on bb-auto
