#!/bin/bash
set -ex

if [ -z "$GOROOT" ]; then
  GO="go"
else
  GO="$GOROOT/bin/go"
fi

pushd src/cmd/makebb
$GO build -covermode=atomic
popd

pushd src/cmd/goanywhere
$GO build -covermode=atomic
popd

MAKEBB=$(pwd)/src/cmd/makebb/makebb
GOANYWHERE=$(pwd)/src/cmd/goanywhere/goanywhere
TMPDIR=$(mktemp -d)
EMPTY_TMPDIR=$(mktemp -d)

pushd $TMPDIR

function ctrl_c() {
  popd
  rm -rf $TMPDIR
  # https://github.com/golang/go/issues/27455
  GOPATH=$EMPTY_TMPDIR $GO clean -cache -modcache
  rm -rf $EMPTY_TMPDIR
}
trap ctrl_c INT

# u-root checked out NOT in $GOPATH.
# Checkout before 1.20+ was required.
(git clone https://github.com/u-root/u-root && cd u-root && git checkout 6ca118b0a77c23ae859cddeee15762d9cd74c63f)
# Pin to commit before Go 1.20 was required. (We test 1.18+.)
(git clone https://github.com/gokrazy/gokrazy && cd gokrazy && git checkout 254af2bf3c82ff9f56e89794b2c146ef9cc85dc6)
# Pin to commit before Go 1.20 was required. (We test 1.18+.)
(git clone https://github.com/hugelgupf/p9 && cd p9 && git checkout 660eb2337e3c1878298fe550ad03248f329eeb72)

# Test workspaces.
go work init ./u-root && go work use ./gokrazy && go work use ./p9

# Test reproducible builds.
GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on $MAKEBB -go-mod=readonly -o bb1 ./u-root/cmds/*/*
GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on $MAKEBB -go-mod=readonly -o bb2 ./u-root/cmds/*/*

cmp bb1 bb2 || (echo "building u-root is not reproducible" && exit 1)
rm bb1 bb2

GOARCH=amd64 GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on $MAKEBB -go-mod=readonly ./u-root/cmds/*/* ./gokrazy/cmd/* ./p9/cmd/*
GOARCH=arm64 GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on $MAKEBB -go-mod=readonly ./u-root/cmds/*/* ./gokrazy/cmd/* ./p9/cmd/*
GOARCH=riscv64 GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on $MAKEBB -go-mod=readonly ./u-root/cmds/*/* ./gokrazy/cmd/* ./p9/cmd/*

# Try an offline build in go workspaces.
# go work vendor is a Go 1.22 feature.
if grep -q -v "go1.21" <<< "$($GO version)" && grep -q -v "go1.20" <<< "$($GO version)";
then
  go work vendor
  GOARCH=amd64 GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR GO111MODULE=on $MAKEBB -go-mod=vendor ./u-root/cmds/*/* ./gokrazy/cmd/* ./p9/cmd/*
fi

# Remove workspace.
rm -rf vendor
rm go.work go.work.sum

$GOANYWHERE -d ./u-root/cmds/*/* ./p9/cmd/* -- $MAKEBB -o $(pwd)

popd
rm -rf $TMPDIR
# https://github.com/golang/go/issues/27455
GOPATH=$EMPTY_TMPDIR $GO clean -cache -modcache
rm -rf $EMPTY_TMPDIR
