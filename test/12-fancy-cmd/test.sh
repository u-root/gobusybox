#!/bin/bash
set -eux

(cd ../../src/cmd/makebb && GO111MODULE=on go build .)
MAKEBB=../../src/cmd/makebb/makebb

# This command intentionally starts with digits and contains characters not
# valid in identifiers (`-`).
for GO111MODULE in on auto;
do
  GO111MODULE=$GO111MODULE $MAKEBB -o bb-$GO111MODULE .
  test -f ./bb-$GO111MODULE

  CMD=$(./bb-$GO111MODULE 12-fancy-cmd);
  test "$CMD" == "12-fancy-cmd" || (echo "12-fancy-cmd not right" && exit 1)
done
