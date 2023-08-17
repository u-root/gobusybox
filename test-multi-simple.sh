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

# Pin to commit before Go 1.20 was required. (We test 1.18+.)
(cd $TMPDIR && git clone https://github.com/gokrazy/gokrazy && cd gokrazy && git checkout 254af2bf3c82ff9f56e89794b2c146ef9cc85dc6)
# Pin to commit before Go 1.20 was required. (We test 1.18+.)
(cd $TMPDIR && git clone https://github.com/hugelgupf/p9 && cd p9 && git checkout 660eb2337e3c1878298fe550ad03248f329eeb72)

# Compile gokrazy and p9 together. Got ideas for what to add here? Let me know.
GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on ./src/cmd/makebb/makebb $TMPDIR/gokrazy/cmd/\* $TMPDIR/p9/cmd/*
GOARCH=arm64 GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on ./src/cmd/makebb/makebb $TMPDIR/gokrazy/cmd/\* $TMPDIR/p9/cmd/*

if grep -q -v "go1.13" <<< "$($GO version)"; then
  GOARCH=riscv64 GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on ./src/cmd/makebb/makebb $TMPDIR/gokrazy/cmd/* $TMPDIR/p9/cmd/*
fi

rm -rf $TMPDIR
