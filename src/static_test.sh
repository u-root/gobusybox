#!/bin/bash

set -eux
declare -r BINARY="${1}"

if [[ -z "${BINARY}" ]]; then
  die "usage: $0 <binary>"
fi

if [[ ! -x "${BINARY}" ]]; then
  die "file must be executable"
fi

# ldd exits with 1 if it's not a dynamic executable.
ldd "${BINARY}" || exit 0
