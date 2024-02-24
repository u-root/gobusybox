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

# Try vendor-based $GOPATH u-root + cpu + p9.
GOPATH_TMPDIR=$(mktemp -d)
function ctrl_c() {
  rm -rf $GOPATH_TMPDIR
}
trap ctrl_c INT

mkdir -p $GOPATH_TMPDIR/src/github.com/u-root
mkdir -p $GOPATH_TMPDIR/src/github.com/hugelgupf

(cd $GOPATH_TMPDIR/src/github.com/u-root && git clone https://github.com/u-root/u-root && cd u-root && git checkout 6ca118b0a77c23ae859cddeee15762d9cd74c63f)
(cd $GOPATH_TMPDIR/src/github.com/u-root && git clone https://github.com/u-root/cpu && cd cpu && git checkout 5529b02a0e41bfc6b3a387c4fa7e7e9cc374a95d && go mod vendor)
(cd $GOPATH_TMPDIR/src/github.com/hugelgupf && git clone https://github.com/hugelgupf/p9 && cd p9 && git checkout 660eb2337e3c1878298fe550ad03248f329eeb72 && go mod vendor)

GOARCH=amd64 GOROOT=$GOROOT GOPATH=$GOPATH_TMPDIR GO111MODULE=off ./src/cmd/makebb/makebb -o bb3 $GOPATH_TMPDIR/src/github.com/u-root/u-root/cmds/*/* $GOPATH_TMPDIR/src/github.com/u-root/cpu/cmds/* $GOPATH_TMPDIR/src/github.com/hugelgupf/p9/cmd/*
GOARCH=arm64 GOROOT=$GOROOT GOPATH=$GOPATH_TMPDIR GO111MODULE=off ./src/cmd/makebb/makebb -o bb3 $GOPATH_TMPDIR/src/github.com/u-root/u-root/cmds/*/* $GOPATH_TMPDIR/src/github.com/u-root/cpu/cmds/* $GOPATH_TMPDIR/src/github.com/hugelgupf/p9/cmd/*
GOARCH=riscv64 GOROOT=$GOROOT GOPATH=$GOPATH_TMPDIR GO111MODULE=off ./src/cmd/makebb/makebb -o bb3 $GOPATH_TMPDIR/src/github.com/u-root/u-root/cmds/*/* $GOPATH_TMPDIR/src/github.com/u-root/cpu/cmds/* $GOPATH_TMPDIR/src/github.com/hugelgupf/p9/cmd/*

rm -rf $GOPATH_TMPDIR bb3
