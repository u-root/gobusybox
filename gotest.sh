#!/bin/bash
set -eux

(cd ./src && GO111MODULE=on go test -cover ./pkg/bb/findpkg/...)
