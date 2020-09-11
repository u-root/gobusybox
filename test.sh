#!/bin/bash
set -eux

./gobuilds.sh
./test-external.sh

(cd src && bazel build //...)

(cd src && bazel build //:bb2)
(cd src && bazel build --platforms=@io_bazel_rules_go//go/toolchain:linux_arm64 //:bb2)
(cd src && bazel build //:bb2_arm64)
