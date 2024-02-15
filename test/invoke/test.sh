#!/bin/bash
set -eux

(cd ../../src/cmd/makebb && GO111MODULE=on go build .)
MAKEBB=../../src/cmd/makebb/makebb

for GO111MODULE in on auto;
do
  GO111MODULE=$GO111MODULE $MAKEBB -o bb-$GO111MODULE ./mod/cmd/helloworld
  test -f ./bb-$GO111MODULE

  HW=$(./bb-$GO111MODULE helloworld);
  test "$HW" == "hello world" || (echo "hello world not right: direct invocation failed" && exit 1)

  ln -s bb-$GO111MODULE helloworld
  HW=$(./helloworld)
  test "$HW" == "hello world" || (echo "hello world not right: symlink invocation failed" && exit 1)
  unlink helloworld

  # add an interpreter file that contains #!/bin/bb #!gobb#!
  echo "#!$(pwd)/bb-$GO111MODULE #!gobb!#" > helloworld
  chmod +x helloworld
  HW=$(./helloworld)
  test "$HW" == "hello world" || (echo "hello world not right: interpreter invocation failed" && exit 1)
  unlink helloworld
done

# check reproducible
cmp bb-on bb-auto
rm bb-on bb-auto
