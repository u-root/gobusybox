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
