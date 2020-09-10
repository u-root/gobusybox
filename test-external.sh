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
(cd $TMPDIR && git clone https://github.com/u-root/u-bmc)
(cd $TMPDIR && git clone https://github.com/hugelgupf/p9)

# Make u-root have modules.
(cd $TMPDIR/u-root && go mod init github.com/u-root/u-root && rm -rf vendor)

# Make u-bmc have modules, and use local u-root.
(cd $TMPDIR/u-bmc && go mod init github.com/u-root/u-bmc)
echo "replace github.com/u-root/u-root => ../u-root" >> $TMPDIR/u-bmc/go.mod

# Make p9 use local u-root.
echo "replace github.com/u-root/u-root => ../u-root" >> $TMPDIR/p9/go.mod

GO111MODULE=auto ./src/cmd/makebb/makebb $TMPDIR/u-root/cmds/*/*
GO111MODULE=on ./src/cmd/makebb/makebb $TMPDIR/u-root/cmds/*/*
GO111MODULE=on ./src/cmd/makebb/makebb $TMPDIR/u-root/cmds/*/* $TMPDIR/gokrazy/cmd/* $TMPDIR/p9/cmd/* $TMPDIR/u-bmc/cmd/*

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
