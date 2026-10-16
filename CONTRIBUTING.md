# Contributing to Go Busybox

Contributions are welcome. Please open issues and pull requests at
https://github.com/u-root/gobusybox.

Go Busybox is part of the u-root project, and shares its
[Code of Conduct](https://github.com/u-root/u-root/wiki/Code-of-Conduct) and
its channels: the `#u-root` channel on the
[Open Source Firmware Slack](https://osfw.slack.com)
(sign up [here](http://slack.osfw.foundation/)), and the
[mailing list](https://groups.google.com/forum/#!forum/u-root).

## Repository layout

The Go module is `src/`, not the repository root. Its path is
`github.com/u-root/gobusybox/src`, and `src/go.sum` is where the go tooling
looks -- run every `go` command from `src/`. The root holds only
documentation, CI configuration, and test fixtures.

- `src/cmd/` -- the three commands: `makebb`, `goanywhere`, `gencmddeps`
- `src/pkg/bb/` -- the rewrite and the generated module
- `src/pkg/bb/findpkg/` -- turning user patterns into package paths
- `src/pkg/golang/` -- the Go toolchain environment
- `test/`, `vendortest/` -- fixture modules, each with its own `go.mod`

## Building and testing

```sh
cd src
go test ./...
```

CI runs on Go 1.25.x and 1.26.x; `src/go.mod` declares `go 1.25.0`. CI also
runs the two integration scripts, which live at the repository root and are
invoked from there, not from `src/`:

```sh
./gobuilds.sh
./test-external-workspaces.sh
```

Note that `test-external-workspaces.sh` runs `go clean -cache -modcache`.
`GOCACHE` is not derived from `GOPATH`, so running that script locally will
destroy your build cache. Point `GOCACHE` at a temporary directory first.

## Do not vendor

**Never run `go mod vendor` in `src/`.** This repository has no `src/vendor/`
directory and CI does not create one. If you make one, every subsequent build
fails with "inconsistent vendoring" until it is deleted, and the failure does
not say that a stray vendor directory is the cause.

`go mod tidy` is fine, and the `Tidy` CI job checks that `go.mod` and `go.sum`
are up to date with it.

## Commit format

Commits take a `<component>: summary` subject, wrapped at 72 columns, where
the component is the package or area touched:

```
bb: detect an input module that collides with the generated module path

More detailed explanation of the change: why, and how. Wrap it to 72
characters.

Signed-off-by: Your Name <you@example.com>
```

Organize changes into small, meaningful commits. Every commit should build and
pass CI on its own.

### Developer sign-off

We follow the [Developer Certificate of Origin](https://developercertificate.org/).
Append a sign-off line to each commit, which `git commit -s` will add:

```
Signed-off-by: Your Name <you@example.com>
```

This is enforced by the DCO check on every pull request.

### Disclosing AI assistance

Go Busybox follows u-root's
[AI usage policy](https://github.com/u-root/u-root/blob/main/AI_POLICY.md).
All AI usage must be disclosed in free form, and AI tools must not be listed
as `Co-authored-by`. Disclose it as its own trailer block, before the
sign-off:

```
Authored by Claude.

Signed-off-by: Your Name <you@example.com>
```

Code written with AI assistance must have been verified by running it, not
merely reviewed.

## Pull requests

Fork the repository, work in a branch, and open a pull request against
https://github.com/u-root/gobusybox. If the pull request closes an issue, note
it as `Fixes #NNN`.

These checks gate a merge: `Tidy` (gofmt and `go mod tidy` drift), `Unit Test`,
`gobuilds` and `test-external-workspaces` on both Go versions, and `DCO`.
`codecov/project` reports a transient failure while other jobs are still
uploading; wait for all jobs before believing it.

## Terminology

[`GLOSSARY.md`](GLOSSARY.md) settles the vocabulary for this codebase -- one
term per concept, and the alternatives to avoid. Please follow it in comments,
documentation, commit messages, and the naming of new identifiers. Existing
identifiers keep their names.

Architectural decisions are recorded in [`docs/adr/`](docs/adr/).
