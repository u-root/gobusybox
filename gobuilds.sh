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
for GO111MODULE in on auto;
do
  GO111MODULE=$GO111MODULE ./makebb ../../../modtest/cmd/dmesg ../../../modtest/cmd/strace
  test -f ./bb || exit 1
  rm ./bb

  GO111MODULE=$GO111MODULE ./makebb ../../../test/mod1/cmd/*
  test -f ./bb || exit 1
  testmod1
  rm ./bb

  # nested modules
  GO111MODULE=$GO111MODULE ./makebb ../../../modtest/cmd/dmesg ../../../modtest/cmd/strace ../../../modtest/nestedmod/cmd/p9ufs
  test -f ./bb || exit 1
  rm ./bb
done

# Make sure `makebb` works completely out of its own tree: there is no go.mod at
# the top of the tree that `go` can fall back on.
cd ../../..

for GO111MODULE in on auto;
do
  GO111MODULE=$GO111MODULE ./src/cmd/makebb/makebb modtest/cmd/dmesg modtest/cmd/strace
  test -f ./bb || exit 1
  rm ./bb

  GO111MODULE=$GO111MODULE ./src/cmd/makebb/makebb modtest/cmd/dmesg modtest/cmd/strace modtest/nestedmod/cmd/p9ufs
  test -f ./bb || exit 1
  rm ./bb

  GO111MODULE=$GO111MODULE ./src/cmd/makebb/makebb test/mod1/cmd/*
  test -f ./bb || exit 1
  testmod1
  rm ./bb

  GO111MODULE=$GO111MODULE ./src/cmd/makebb/makebb test/mod5/cmd/mod5hello test/mod6/cmd/mod6hello
  test -f ./bb
  rm ./bb
done
