# GOPATH mode is not supported

Go Busybox used to accept commands resolved in GOPATH mode (`GO111MODULE=off`)
as well as commands in modules and workspaces. It no longer does: every command
must belong to a Go module. `makebb` and `goanywhere` reject `GO111MODULE=off`
outright, and reject any command that `go/packages` reports with no module.

This is not deprecation for fashion's sake. GOPATH mode is the one input mode
that the generated tree cannot represent.

The generated tree vendors every dependency into a single flat directory, which
can hold exactly one `github.com/hugelgupf/p9/fsimpl/templatefs`. GOPATH mode
has no such constraint: an import path is resolved by walking up from the
importing package looking for a `vendor/` directory, so two commands in one
build can each get their own copy of one import path at two different versions,
and `go/packages` reports them under distinct paths
(`github.com/u-root/cpu/vendor/github.com/hugelgupf/p9/...` versus
`github.com/hugelgupf/p9/...`).

Stripping the nested-vendor prefix to flatten those into the vendor directory
collapses both onto one path. Whichever package was visited first won and the
other was silently dropped — and the visit order is the sorted order of the
command import paths, which is an arbitrary basis on which to choose a library
version. This was not hypothetical. Building u-root, `u-root/cpu` and
`hugelgupf/p9` together from one GOPATH produced a tree in which `cpu/client`
failed to compile:

```
vendor/github.com/u-root/cpu/client/cpio9p.go:44:13: undefined: templatefs.XattrUnimplemented
vendor/github.com/u-root/cpu/client/cpio9p.go:46:13: undefined: templatefs.NilSyncer
```

`cpu` required p9 at `e6037077`, which has those symbols. The GOPATH tree also
held a standalone p9 at `660eb23`, ten commits older, which does not. The older
copy sorted first and won. Comparing the three repositories' `go.mod` files at
the versions that test pinned, **19 import paths were required at differing
versions**; p9 was simply the only one whose API had diverged enough to break
the build. The other eighteen were being resolved the same arbitrary way, in
silence.

In module mode the problem cannot arise. The go tool has already chosen exactly
one version per import path — by minimal version selection within a module, or
across all members of a workspace — before Go Busybox sees anything, and
`go list` reports the real, flat import path for a vendored package rather than
a nested one. The flat vendor directory can therefore always hold the whole
build.

## Consequences

- A build that previously succeeded in GOPATH mode now fails. That is the
  point: it previously succeeded by giving some command a version of a library
  it was never compiled against. The error names the offending commands and
  points at `goanywhere`.

- Commands spanning several repositories need a `go.work`. `goanywhere`
  generates one, so this is a change in invocation, not in capability.

- The flattening step is gone, along with the synthetic single-package modules
  that existed to give module-less packages something to be attributed to. A
  package with no module, or one whose module path is not a prefix of its
  import path, is now an error rather than a guess.

- Go Busybox's own test suite no longer needs a checkout inside `$GOPATH` as a
  real directory. The GOPATH-mode cases were the only ones that did, and they
  failed for everyone who ran `go test ./...` from an ordinary checkout.

- Two CI jobs (`gobuilds-gopath`, `test-external-gopath`) and the scripts they
  ran are deleted.
