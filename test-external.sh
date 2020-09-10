#!/bin/bash
set -eux

cd src/cmd/makebb

go generate
go build

cd ../../..

TMPDIR=$(mktemp -d)

function ctrl_c() {
  rm -rf $TMPDIR
}
trap ctrl_c INT

# u-root checked out NOT in $GOPATH.
(cd $TMPDIR && git clone https://github.com/u-root/u-root)
(cd $TMPDIR && git clone https://github.com/gokrazy/gokrazy)

# Make u-root have modules.
(cd $TMPDIR/u-root && go mod init github.com/u-root/u-root && go mod vendor)
# Disarm vendor directory.
(cd $TMPDIR/u-root && mv vendor vendor2)

GO111MODULE=auto ./src/cmd/makebb/makebb $TMPDIR/u-root/cmds/*/*
GO111MODULE=on ./src/cmd/makebb/makebb $TMPDIR/u-root/cmds/*/*
GO111MODULE=on ./src/cmd/makebb/makebb $TMPDIR/u-root/cmds/*/* $TMPDIR/gokrazy/cmd/*

# This should work as is, too. It'll pull it straight from the internet.
#GO111MODULE=on ./src/cmd/makebb/makebb github.com/u-root/u-root/cmds/...
rm -rf $TMPDIR


# Try vendor-based $GOPATH u-root.
GOPATH_TMPDIR=$(mktemp -d)
function ctrl_c() {
  rm -rf $GOPATH_TMPDIR
}
trap ctrl_c INT

(cd $GOPATH_TMPDIR && GOPATH=$GOPATH_TMPDIR GO111MODULE=off go get -u github.com/u-root/u-root)
GOPATH=$GOPATH_TMPDIR GO111MODULE=off ./src/cmd/makebb/makebb github.com/u-root/u-root/cmds/...
rm -rf $GOPATH_TMPDIR
