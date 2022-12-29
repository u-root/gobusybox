#!/bin/bash
set -eux

# all the go module builds should still work in $GOPATH
./gobuilds.sh

cd src/cmd/makebb
GO111MODULE=off ./makebb ../../../vendortest/cmd/*

cd ../../..
GO111MODULE=off ./src/cmd/makebb/makebb vendortest/cmd/*

TMPDIR=$(mktemp -d)
function ctrl_c() {
  rm -rf $TMPDIR
  # https://github.com/golang/go/issues/27455
  GOPATH=$EMPTY_TMPDIR $GO clean -cache -modcache
  rm -rf $EMPTY_TMPDIR
}
trap ctrl_c INT

(cd $TMPDIR && git clone https://github.com/u-root/u-root)
(cd ./src && GO111MODULE=on go test -cover ./pkg/bb/findpkg/... --uroot-source=$TMPDIR/u-root)
rm -rf $TMPDIR
