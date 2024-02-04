#!/bin/bash
set -eux

TMPDIR=$(mktemp -d)
function ctrl_c() {
  rm -rf $TMPDIR
  # https://github.com/golang/go/issues/27455
  GOPATH=$EMPTY_TMPDIR $GO clean -cache -modcache
  rm -rf $EMPTY_TMPDIR
}
trap ctrl_c INT

# Checkout before 1.20+ was required.
(cd $TMPDIR && git clone https://github.com/u-root/u-root && cd u-root && git checkout 6ca118b0a77c23ae859cddeee15762d9cd74c63f)
(cd ./src && GO111MODULE=on go test -cover ./pkg/bb/findpkg/... --uroot-source=$TMPDIR/u-root)
rm -rf $TMPDIR
