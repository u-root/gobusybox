#!/bin/bash
set -eux

(cd ./test/normaldeps && ./test.sh)
(cd ./test/diamonddep && ./test.sh)
(cd ./test/requestconflict && ./test.sh)
(cd ./test/nested && ./test.sh)
(cd ./test/implicitimport && ./test.sh)
