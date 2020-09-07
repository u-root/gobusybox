#!/bin/bash
set -eux

cd src/cmd/makebb
go build

# This uses the go.mod in src/
GO111MODULE=on ./makebb ../../../modtest/cmd/dmesg ../../../modtest/cmd/strace
GO111MODULE=auto ./makebb ../../../modtest/cmd/dmesg ../../../modtest/cmd/strace

# nested modules
#GO111MODULE=on ./makebb ../../../modtest/cmd/dmesg ../../../modtest/cmd/strace ../../../modtest/nestedmod/p9ufs
#GO111MODULE=auto ./makebb ../../../modtest/cmd/dmesg ../../../modtest/cmd/strace ../../../modtest/nestedmod/p9ufs

# Make sure `makebb` works completely out of its own tree: there is no go.mod at
# the top of the tree that `go` can fall back on.
cd ../../..
GO111MODULE=on ./src/cmd/makebb/makebb modtest/cmd/dmesg modtest/cmd/strace
GO111MODULE=auto ./src/cmd/makebb/makebb modtest/cmd/dmesg modtest/cmd/strace

#GO111MODULE=on ./src/cmd/makebb/makebb modtest/cmd/dmesg modtest/cmd/strace modtest/nestedmod/p9ufs
#GO111MODULE=auto ./src/cmd/makebb/makebb modtest/cmd/dmesg modtest/cmd/strace modtest/nestedmod/p9ufs
