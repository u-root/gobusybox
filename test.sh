#!/bin/bash
set -eux

./gobuilds.sh
./test-external.sh
