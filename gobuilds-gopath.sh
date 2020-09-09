#!/bin/bash
set -eux

# all the go module builds should still work in $GOPATH
./gobuilds.sh

cd src/cmd/makebb
GO111MODULE=off ./makebb ../../../vendortest/cmd/*

cd ../../..
GO111MODULE=off ./src/cmd/makebb/makebb vendortest/cmd/*
