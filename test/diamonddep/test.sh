#!/bin/bash
set -eux

(cd ../../src/cmd/makebb && GO111MODULE=on go build .)
MAKEBB=../../src/cmd/makebb/makebb

WANT="test/diamonddep/mod1/hello: test/diamonddep/mod1/hello
test/diamonddep/mod2/hello: test/diamonddep/mod2/hello
test/diamonddep/mod2/exthello: test/diamonddep/mod2/exthello: test/diamonddep/mod1/hello and test/diamonddep/mod3/hello"

for GO111MODULE in on auto;
do
  GO111MODULE=$GO111MODULE $MAKEBB -o bb-$GO111MODULE ./mod1/cmd/*
  test -f ./bb-$GO111MODULE

  HW=$(./bb-$GO111MODULE helloworld);
  test "$HW" == "hello world" || (echo "helloworld not right" && exit 1)

  HWDEPS=$(./bb-$GO111MODULE hellowithdep);
  test "$HWDEPS" == "$WANT" || (echo "hellowithdep not right" && exit 1)
done

# check reproducible
cmp bb-on bb-auto
rm bb-on bb-auto
