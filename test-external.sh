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
(cd $TMPDIR && git clone https://github.com/u-root/u-bmc)
(cd $TMPDIR && git clone https://github.com/hugelgupf/p9)

# Make u-root have modules.
(cd $TMPDIR/u-root && rm -rf vendor)

# Make u-bmc have modules, and use local u-root.
(cd $TMPDIR/u-bmc && $GO mod init github.com/u-root/u-bmc)
(cd $TMPDIR/u-bmc && touch config/i_agree_to_the_acme_terms)
# fake ssh key, whatever.
(cd $TMPDIR/u-bmc && touch ssh_keys.pub)
(cd $TMPDIR/u-bmc && $GO generate ./config)
echo "replace github.com/u-root/u-root => ../u-root" >> $TMPDIR/u-bmc/go.mod

# Make p9 use local u-root.
echo "replace github.com/u-root/u-root => ../u-root" >> $TMPDIR/p9/go.mod

GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=auto ./src/cmd/makebb/makebb -o bb1 $TMPDIR/u-root/cmds/*/*
GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on ./src/cmd/makebb/makebb -o bb2 $TMPDIR/u-root/cmds/*/*

cmp bb1 bb2 || (echo "building u-root is not reproducible" && exit 1)
rm bb1 bb2

GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on ./src/cmd/makebb/makebb $TMPDIR/u-root/cmds/*/* $TMPDIR/gokrazy/cmd/* $TMPDIR/p9/cmd/*
GOARM=5 GOARCH=arm GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on ./src/cmd/makebb/makebb $TMPDIR/u-root/cmds/core/* $TMPDIR/u-bmc/cmd/* $TMPDIR/u-bmc/platform/quanta-f06-leopard-ddr3/cmd/*

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
GOROOT=$GOROOT GOPATH=$GOPATH_TMPDIR GO111MODULE=off ./src/cmd/makebb/makebb -o bb3 github.com/u-root/u-root/cmds/...

rm -rf $GOPATH_TMPDIR
