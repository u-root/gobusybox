#!/bin/bash
set -eux

# all the go module builds should still work in $GOPATH
./gobuilds.sh

cd src/cmd/makebb

# This uses the go.mod in src/
GO111MODULE=off ./makebb ../../../vendortest/cmd/dmesg ../../../vendortest/cmd/strace

# mix vendor-based cmds with mod-based cmds. only works with GO111MODULE=off iff
# dependencies are in $GOPATH.
#GO111MODULE=off ./makebb ../../../vendortest/cmd/dmesg ../../../modtest/cmd/strace
#GO111MODULE=off ./makebb ../../../modtest/cmd/dmesg ../../../modtest/cmd/strace
#GO111MODULE=off ./makebb ../../../modtest/cmd/dmesg ../../../vendortest/cmd/strace

# Make sure `makebb` works completely out of its own tree: there is no go.mod at
# the top of the tree that `go` can fall back on.
cd ../../..
GO111MODULE=off ./src/cmd/makebb/makebb vendortest/cmd/dmesg vendortest/cmd/strace

#GO111MODULE=off ./src/cmd/makebb/makebb vendortest/cmd/dmesg modtest/cmd/strace
#GO111MODULE=off ./src/cmd/makebb/makebb modtest/cmd/dmesg vendortest/cmd/strace
#GO111MODULE=off ./src/cmd/makebb/makebb modtest/cmd/dmesg modtest/cmd/strace
