#!/bin/bash
set -eux

./gobuilds.sh
./test-external.sh

(cd src && bazel build //...)
