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

(cd $TMPDIR && git clone https://github.com/gokrazy/gokrazy && cd gokrazy && git checkout 1c3bb2365452542d52263bd4eeaafec0b83e90e2)
# Pin to commit before Go 1.18+ was required.
(cd $TMPDIR && git clone https://github.com/hugelgupf/p9 && cd p9 && git checkout 8313e2fcf87db241a29570a9ed75eab866c104bf)

# Compile gokrazy and p9 together. Got ideas for what to add here? Let me know.
GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on ./src/cmd/makebb/makebb $TMPDIR/gokrazy/cmd/\* $TMPDIR/p9/cmd/*
GOARCH=arm64 GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on ./src/cmd/makebb/makebb $TMPDIR/gokrazy/cmd/\* $TMPDIR/p9/cmd/*

if grep -q -v "go1.13" <<< "$($GO version)"; then
  GOARCH=riscv64 GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on ./src/cmd/makebb/makebb $TMPDIR/gokrazy/cmd/* $TMPDIR/p9/cmd/*
fi

rm -rf $TMPDIR
