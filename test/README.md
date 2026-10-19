Test inputs for `makebb`: directories whose dependency structure, or whose
source, is something the rewrite has to get right.

Not every one is a module. `diamonddep` and `normaldeps` are containers for
several, `injectldvar` belongs to this directory's own module, and the rest
declare their own.

Most are driven by `all_test.go`, which builds a busybox from the commands a
case names, runs each one, and compares its output. `resolve-modules` is the
exception, and belongs to the `findpkg` unit tests instead.

```sh
go test ./...              # builds makebb, then runs these
go test -external ./...    # also fetches u-root, gokrazy and p9
```

`external_test.go` holds the `-external` cases: real third-party trees built
through a Go workspace, and `gomodbb` building from the module proxy with no
source tree at all. Every one of them takes its source from the module proxy,
so the suite needs no version control tool installed.

| Directory | What it covers |
| --- | --- |
| `diamonddep/` | Mutually and diamondly dependent modules |
| `normaldeps/` | An import path that does not match its directory |
| `nameconflict/` | Names the rewrite must not collide with |
| `implicitimport/` | An import the rewrite has to add |
| `goembed/` | `//go:embed`, whose files must reach the generated tree |
| `injectldvar/` | `-ldflags -X` reaching a variable through the rewrite |
| `12-fancy-cmd/` | A command whose name is not a valid Go identifier |
| `nested/` | A module inside another module's directory |
| `resolve-modules/` | Resolving patterns to modules, for `findpkg` |

### diamonddep

Three modules depending on each other mutually and diamondly, with no package
cycle, which is valid Go and has to keep working:

```
mod1/cmd/hellowithdep -> mod1/pkg/hello
                      -> mod2/pkg/hello
                      -> mod2/pkg/exthello -> mod1/pkg/hello
                                           -> mod3/pkg/hello
```

`mod2` depends back on `mod1`, so neither module can be built in isolation.

### normaldeps

`mod1` imports `.../normaldeps/mod2/v2/pkg/hello`, whose `/v2` has no matching
directory: the module lives in `mod2/`. Also covers a command that is not the
only one in its module, since `mod1` holds both `helloworld` and `getppid`.

### nameconflict

One command written to collide with everything the rewrite generates:

- `busyboxInit0` and `busyboxInit1` are already taken, so the busybox init
  made from this command's `init` has to become `busyboxInit2`.
- `registeredInit` and `registeredMain` are already taken too.
- A variable named `log` shadows package `log`, and an import is aliased
  `bbmain`.
- The same package is imported twice under two names, and under a third name
  in a second file of the same package.

### implicitimport

`defaultlog.Default()` returns a `*log.Logger`, but the command never imports
`log`. The rewrite has to add that import, and add it exactly once even though
the call appears twice.

### goembed

`//go:embed foo/*.txt`. The embedded files are not Go source, so copying only
the source would silently lose them. Skipped on go1.15, which predates
`go:embed`.

### injectldvar

Built with `-ldflags -X`, naming a variable by its original package path. The
rewrite moves that variable into another package, so the test pins that the
flag still reaches it. This one has no `go.mod` of its own, which is why that
path is `.../test/injectldvar`.

### nested

A module, `nestedmod`, inside another module's directory tree. `cmd/dmesg` and
`cmd/strace` belong to the outer module; `nestedmod/cmd/p9ufs` does not, so a
module's directory tree is not its package set.

Building commands from both at once needs the `go.work` here, since `makebb`
requires Go modules and no longer has a GOPATH mode to fall back on. Remove it
and the case fails: the go tool reports that the main module does not contain
`nestedmod/cmd/p9ufs`.

The outer module alone is what the "How It Works" section of the top-level
`README.md` builds.

### resolve-modules

A `tools`-tagged file importing commands from `u-root` and `p9`, used by
`src/pkg/bb/findpkg` to test turning patterns into module paths. Nothing here
is built into a busybox.
