#!/bin/bash
set -eux

cd src/cmd/makebb

go generate
go build

# This uses the go.mod in src/
GO111MODULE=on ./makebb ../../../modtest/cmd/dmesg ../../../modtest/cmd/strace
GO111MODULE=auto ./makebb ../../../modtest/cmd/dmesg ../../../modtest/cmd/strace
GO111MODULE=off ./makebb ../../../vendortest/cmd/dmesg ../../../vendortest/cmd/strace

# This has no go.mod!
cd ../../..
GO111MODULE=on ./src/cmd/makebb/makebb modtest/cmd/dmesg modtest/cmd/strace
GO111MODULE=auto ./src/cmd/makebb/makebb modtest/cmd/dmesg modtest/cmd/strace
GO111MODULE=off ./src/cmd/makebb/makebb vendortest/cmd/dmesg vendortest/cmd/strace
