#!/bin/bash
set -eux

(cd ../../src/cmd/makebb && GO111MODULE=on go build .)
MAKEBB=../../src/cmd/makebb/makebb

WANT="test/diamonddep/mod1/hello: test/diamonddep/mod1/hello
test/diamonddep/mod2/hello: test/diamonddep/mod2/hello
test/diamonddep/mod2/exthello: test/diamonddep/mod2/exthello: test/diamonddep/mod1/hello and test/diamonddep/mod3/hello"

for GO111MODULE in on auto;
do
  GO111MODULE=$GO111MODULE $MAKEBB ./mod1/cmd/*
  test -f ./bb || exit 1

  HW=$(./bb helloworld);
  test "$HW" == "hello world" || (echo "helloworld not right" && exit 1)

  HWDEPS=$(./bb hellowithdep);
  test "$HWDEPS" == "$WANT" || (echo "hellowithdep not right" && exit 1)

  rm ./bb
done
