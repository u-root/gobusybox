#!/bin/bash
set -eux

(cd src && GO111MODULE=on go mod vendor)

# all the go module builds should still work in $GOPATH
./gobuilds.sh

cd src/cmd/makebb
GO111MODULE=off ./makebb ../../../vendortest/cmd/*

cd ../../..
GO111MODULE=off ./src/cmd/makebb/makebb vendortest/cmd/*
