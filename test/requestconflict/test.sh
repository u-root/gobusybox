#!/bin/bash
set -ux

(cd ../../src/cmd/makebb && GO111MODULE=on go build .)
MAKEBB=../../src/cmd/makebb/makebb

for GO111MODULE in on auto;
do
  if GO111MODULE=$GO111MODULE $MAKEBB ./mod5/cmd/mod5hello ./mod6/cmd/mod6hello; then
    echo "makebb should have failed for conflict"
    exit 1
  fi
done

# all of the following should succeed.

set -e
cp ./mod5/go.mod ./mod5/go.mod.hold

# solution should work!
echo "replace github.com/u-root/gobusybox/test/requestconflict/mod6 => ../mod6" >> ./mod5/go.mod

for GO111MODULE in on auto;
do
  GO111MODULE=$GO111MODULE $MAKEBB ./mod5/cmd/mod5hello ./mod6/cmd/mod6hello
  test -f ./bb
  rm ./bb
done

mv ./mod5/go.mod.hold ./mod5/go.mod
