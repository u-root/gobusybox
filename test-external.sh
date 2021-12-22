#!/bin/bash
set -ex

if [ -z "$GOROOT" ]; then
  GO="go"
else
  GO="$GOROOT/bin/go"
fi

cd src/cmd/makebb

$GO generate
$GO build

cd ../../..

TMPDIR=$(mktemp -d)
EMPTY_TMPDIR=$(mktemp -d)

function ctrl_c() {
  rm -rf $TMPDIR
  # https://github.com/golang/go/issues/27455
  GOPATH=$EMPTY_TMPDIR $GO clean -cache -modcache
  rm -rf $EMPTY_TMPDIR
}
trap ctrl_c INT

# u-root checked out NOT in $GOPATH.
(cd $TMPDIR && git clone https://github.com/u-root/u-root)
(cd $TMPDIR && git clone https://github.com/gokrazy/gokrazy)
(cd $TMPDIR && git clone https://github.com/hugelgupf/p9)

# Make u-root have modules.
(cd $TMPDIR/u-root && rm -rf vendor)

GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=auto ./src/cmd/makebb/makebb -o bb1 $TMPDIR/u-root/cmds/*/*
GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on ./src/cmd/makebb/makebb -o bb2 $TMPDIR/u-root/cmds/*/*

cmp bb1 bb2 || (echo "building u-root is not reproducible" && exit 1)
rm bb1 bb2

GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on ./src/cmd/makebb/makebb $TMPDIR/u-root/cmds/*/* $TMPDIR/gokrazy/cmd/* $TMPDIR/p9/cmd/*
GOARCH=arm64 GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on ./src/cmd/makebb/makebb $TMPDIR/u-root/cmds/*/* $TMPDIR/gokrazy/cmd/* $TMPDIR/p9/cmd/*

if grep -q -v "go1.13" <<< "$($GO version)"; then
  GOARCH=riscv64 GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on ./src/cmd/makebb/makebb $TMPDIR/u-root/cmds/*/* $TMPDIR/gokrazy/cmd/* $TMPDIR/p9/cmd/*
fi

rm -rf $TMPDIR
# https://github.com/golang/go/issues/27455
GOPATH=$EMPTY_TMPDIR $GO clean -cache -modcache
rm -rf $EMPTY_TMPDIR

# Try vendor-based $GOPATH u-root.
GOPATH_TMPDIR=$(mktemp -d)
function ctrl_c() {
  rm -rf $GOPATH_TMPDIR
}
trap ctrl_c INT

(cd $GOPATH_TMPDIR && GOPATH=$GOPATH_TMPDIR GO111MODULE=off $GO get -u github.com/u-root/u-root)
GOROOT=$GOROOT GOPATH=$GOPATH_TMPDIR GO111MODULE=off ./src/cmd/makebb/makebb -o bb3 $GOPATH_TMPDIR/src/github.com/u-root/u-root/cmds/*/*
GOARCH=arm64 GOROOT=$GOROOT GOPATH=$GOPATH_TMPDIR GO111MODULE=off ./src/cmd/makebb/makebb -o bb3 $GOPATH_TMPDIR/src/github.com/u-root/u-root/cmds/*/*

if grep -q -v "go1.13" <<< "$($GO version)"; then
  GOARCH=riscv64 GOROOT=$GOROOT GOPATH=$GOPATH_TMPDIR GO111MODULE=off ./src/cmd/makebb/makebb -o bb3 $GOPATH_TMPDIR/src/github.com/u-root/u-root/cmds/*/*
fi

rm -rf $GOPATH_TMPDIR bb3
