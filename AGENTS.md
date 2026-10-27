# Notes for coding agents

Start with [`CONTRIBUTING.md`](CONTRIBUTING.md) for layout, commit format and
sign-off. This file holds only what is easy to get wrong.

## Vocabulary

[`GLOSSARY.md`](GLOSSARY.md) settles one term per concept, with the
alternatives to avoid. Follow it in comments, documentation and commit
messages, and when naming new identifiers. Do not rename existing identifiers
to match it.

Architectural decisions are in [`docs/adr/`](docs/adr/). Record a decision
there rather than in the glossary, which is a term list and nothing more.

## Build and test

```sh
cd src
go test ./...
go build ./...
```

Run every `go` command from `src/`. The repository root is not a module.

## Traps

**The module is `src/`, not the repository root.** `go.mod` and `go.sum` live
there. Tooling that assumes a root module will silently do nothing useful --
this is why the CI Go cache never hits.

**Never run `go mod vendor` in `src/`.** There is no `src/vendor/` and CI does
not create one. Creating one makes every later build fail with "inconsistent
vendoring", and the error does not say a stray vendor directory is the cause.
`go mod tidy` is fine and is checked by CI.

**The integration tests live in `test/`, not `src/`.** `cd test && go test
./...` builds `makebb` itself and runs the fixtures. Add `-external` to also
fetch u-root, gokrazy and p9; it is off by default because it needs the
network and a few minutes. Nothing needs building or passing in by hand, and
nothing needs git: the source comes from the module proxy, with `GOVCS=*:off`
to keep it that way.

**`makebb`'s flag names differ from its Go field names.** Generate-only is
`-g`, and the output directory is `-gen-dir`, which sets `Opts.GenSrcDir`.
There is no `-gen-only` or `-gen-src-dir`.

**`goanywhere -tmp=false` writes `go.work` into the current directory** and
leaves `GOWORK` empty. Read `go.work`, not `$GOWORK`.

**`golang.Environ` overwrites `PATH` with `$GOROOT/bin`.** A binary spawned
through `Compiler.Path` runs without `/usr/bin`, so it may have shell builtins
only. This matters when writing tests that stand up a fake compiler.

**Cross-architecture testing needs `binfmt_misc`, not a wrapper on `PATH`.**
`cd test && GOARCH=arm64 go test ./...` runs the whole suite on another
architecture, because `GOARCH` reaches the go tool, so the test binary,
`makebb` and every generated busybox are built for the target while the
compiler stays native. But the suite *spawns* foreign binaries rather than
running one -- the test binary execs `makebb`, which execs the busybox -- and
qemu-user hands `execve` to the host kernel instead of re-entering itself. A
nested exec then fails with `exec format error` unless the kernel knows the
interpreter. The `test-crossarch` job registers one.

**Building the test binary for another architecture and running it under qemu
by hand proves nothing.** `GOARCH=arm64 go test -c` then `qemu-aarch64
./test.arm64` passes in seconds, and every binary it tests is still the host's:
`execve` passes through, so the `go` it spawns runs natively and targets
natively. Measured from inside such a run: `runtime.GOARCH = arm64` while
`go env GOARCH` says `amd64`. Set `GOARCH` on the whole invocation instead.

**On Ubuntu 26.04 the package is `qemu-user`, not `qemu-user-static`.** The
latter no longer exists, so the recipe every other project uses fails on the
runner this repository pins. `qemu-user` *is* the static build. Installing it
pulls `ubuntu-virt` and the whole `qemu-system` stack, about 100 MB to get
11 MB of use, which is why CI unpacks the `.deb` instead.

**The kernel decodes the `\xNN` in a binfmt_misc registration, so no shell
may.** `printf ":q:M::$magic:$mask:$interp:POCF\n"` puts the escapes in the
*format*, and bash's `printf` decodes `\x` while dash's does not -- so the
same line writes text under one shell and raw NUL bytes under the other.
Under bash the kernel then registers something other than what was written,
matching host binaries, and every later command dies with `exec format
error`, the runner's own `bash` and `node` included. The job cannot report
this, because reporting it needs a program it can no longer exec. Pass the
string as an argument, `printf '%s\n' "$reg"`, and check it still holds
literal `\x7f\x45\x4c\x46` *before* writing.
