#!/bin/bash
set -eux

./gobuilds.sh
./test-external.sh

bazel build //src/...

bazel build --platforms=@io_bazel_rules_go//go/toolchain:linux_arm64 //src:bb
