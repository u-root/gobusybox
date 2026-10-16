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

pushd src/cmd/gomodbb
$GO build -covermode=atomic
popd

MAKEBB=$(pwd)/src/cmd/makebb/makebb
GOANYWHERE=$(pwd)/src/cmd/goanywhere/goanywhere
GOMODBB=$(pwd)/src/cmd/gomodbb/gomodbb
TMPDIR=$(mktemp -d)
EMPTY_TMPDIR=$(mktemp -d)

# The revision gomodbb fetches below, so that it and the clone cannot drift
# apart and start testing two different trees.
UROOT_REV=6ca118b0a77c23ae859cddeee15762d9cd74c63f

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
(git clone https://github.com/u-root/u-root && cd u-root && git checkout $UROOT_REV)
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

# gomodbb fetches its own source, so unlike everything above it needs neither a
# checkout nor a workspace. Run it in an empty directory, with no module and no
# go.work anywhere above it, so that a regression where it quietly relies on one
# cannot pass.
#
# Fetching the revision cloned above, rather than a branch, keeps this from
# failing the day u-root's default branch moves, and asks the question worth
# asking: the same source reached two ways should build the same commands.
GOMODBB_TMPDIR=$(mktemp -d)
pushd $GOMODBB_TMPDIR

GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR $GOMODBB -l github.com/u-root/u-root/cmds/core/...@$UROOT_REV > listed.txt
grep -q '^github.com/u-root/u-root/cmds/core/ls$' listed.txt || (echo "gomodbb -l did not list ls" && exit 1)

GOROOT=$GOROOT GOPATH=$EMPTY_TMPDIR $GOMODBB -o bb-gomodbb \
  github.com/u-root/u-root/cmds/core/dmesg@$UROOT_REV \
  github.com/u-root/u-root/cmds/core/ls@$UROOT_REV

# Dispatch by argv[1], since argv[0] is not a command name here.
./bb-gomodbb ls > /dev/null || (echo "busybox built by gomodbb cannot run ls" && exit 1)

popd
rm -rf $GOMODBB_TMPDIR

rm -rf $TMPDIR
# https://github.com/golang/go/issues/27455
GOPATH=$EMPTY_TMPDIR $GO clean -cache -modcache
rm -rf $EMPTY_TMPDIR
