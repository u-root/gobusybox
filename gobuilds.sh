#!/bin/bash
set -eux

cd src/cmd/makebb
go build

function testmod1() {
  BPID=$(echo $$);
  GETPPID=$(./bb getppid);
  test "$BPID" == "$GETPPID" || (echo "PIDs not the same" && exit 1)

  HW=$(./bb helloworld);
  test "$HW" == "hello world" || (echo "hello world not right" && exit 1)
}

# This uses the go.mod in src/
GO111MODULE=on ./makebb ../../../modtest/cmd/dmesg ../../../modtest/cmd/strace
test -f ./bb || exit 1
rm ./bb

GO111MODULE=auto ./makebb ../../../modtest/cmd/dmesg ../../../modtest/cmd/strace
test -f ./bb || exit 1
rm ./bb

GO111MODULE=on ./makebb ../../../test/mod1/cmd/helloworld ../../../test/mod1/cmd/getppid ../../../test/mod1/cmd/hellowithdep
test -f ./bb || exit 1
testmod1
rm ./bb

GO111MODULE=auto ./makebb ../../../test/mod1/cmd/helloworld ../../../test/mod1/cmd/getppid ../../../test/mod1/cmd/hellowithdep
test -f ./bb || exit 1
testmod1
rm ./bb

# nested modules
GO111MODULE=on ./makebb ../../../modtest/cmd/dmesg ../../../modtest/cmd/strace ../../../modtest/nestedmod/cmd/p9ufs
test -f ./bb || exit 1
rm ./bb

GO111MODULE=auto ./makebb ../../../modtest/cmd/dmesg ../../../modtest/cmd/strace ../../../modtest/nestedmod/cmd/p9ufs
test -f ./bb || exit 1
rm ./bb

# Make sure `makebb` works completely out of its own tree: there is no go.mod at
# the top of the tree that `go` can fall back on.
cd ../../..
GO111MODULE=on ./src/cmd/makebb/makebb modtest/cmd/dmesg modtest/cmd/strace
test -f ./bb || exit 1
rm ./bb

GO111MODULE=auto ./src/cmd/makebb/makebb modtest/cmd/dmesg modtest/cmd/strace
test -f ./bb || exit 1
rm ./bb

GO111MODULE=on ./src/cmd/makebb/makebb modtest/cmd/dmesg modtest/cmd/strace modtest/nestedmod/cmd/p9ufs
test -f ./bb || exit 1
rm ./bb

GO111MODULE=auto ./src/cmd/makebb/makebb modtest/cmd/dmesg modtest/cmd/strace modtest/nestedmod/cmd/p9ufs
test -f ./bb || exit 1
rm ./bb

GO111MODULE=on ./src/cmd/makebb/makebb test/mod1/cmd/helloworld test/mod1/cmd/getppid test/mod1/cmd/hellowithdep
test -f ./bb || exit 1
testmod1
rm ./bb

GO111MODULE=auto ./src/cmd/makebb/makebb test/mod1/cmd/helloworld test/mod1/cmd/getppid test/mod1/cmd/hellowithdep
test -f ./bb || exit 1
testmod1
rm ./bb
