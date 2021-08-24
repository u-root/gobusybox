#!/bin/bash
set -eux

# This one hasn't been migrated to the go test yet.
(cd ./test/requestconflict && ./test.sh)
(cd ./test/nested && ./test.sh)
(cd ./test/implicitimport && ./test.sh)
(cd ./test/nameconflict && ./test.sh)
(cd ./test/12-fancy-cmd && ./test.sh)
(cd ./test/invoke && ./test.sh)
