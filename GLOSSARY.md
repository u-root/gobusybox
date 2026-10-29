# Go Busybox

Go Busybox compiles many independent Go commands into a single binary that
dispatches to the right one at runtime. It does this by rewriting each
command's source into a side-effect-free package and generating a new main
program around them.

This glossary binds prose -- comments, documentation, commit messages, review
-- and the naming of new identifiers. It does not require renaming existing
ones: `golang.Environ`, the `bb` package and `GBB_PATH` keep their names, and
are referred to here by them. Where a term names something that exists in the
code, the entry says what the identifier is called.

It is written for both humans and coding agents, weighted to agents. That is
why each entry carries an _Avoid_ list naming words that are plausible but
wrong here, including some this repository has never used: an agent arriving
without the project's history is the reader most likely to reach for them.

## Language

### The binary and its commands

**Go Busybox**:
The project and its tools -- `makebb`, `goanywhere`, `gencmddeps` -- and the
source-to-source transformation they perform. Always capitalised.
_Avoid_: gobusybox outside the import path, Go busybox

**busybox**:
A single binary containing many Go commands, which selects one to run from its
invocation arguments. Lowercase, and usually "a busybox" or "the busybox", to
distinguish the artifact from the project that builds it.
_Avoid_: bb (that is the default output filename, not the concept), multibinary

**Command**:
A `package main` that becomes one entry in a busybox. A directory only
qualifies as a command if its package is named `main` and it has at least one
Go file surviving the build constraints. The type is `bbinternal.Package`.
_Avoid_: binary, executable, tool, applet

**Dispatch**:
Choosing which command to run, from `argv[0]` (usually via a symlink to the
busybox) or, when `argv[0]` names no registered command and no default is
registered, from `argv[1]`. Plan 9 does not take the `argv[1]` fallback. No
identifier: the entry point is `bbmain.Run`.
_Avoid_: routing, multiplexing

**Shellbang**:
A script whose interpreter line names the busybox and a command, e.g.
`#!/bbin/bb #!/bbin/date`. Plan 9 passes these arguments differently from Unix.
_Avoid_: shebang, hashbang

### The transformation

**Rewrite**:
The source-to-source transformation that turns a command into an importable
package with no global side effects. Every command is rewritten, by
`bbinternal.Package.Rewrite`. A library is rewritten only when `-lazy-init`
names it or something it imports, by `bbinternal.Package.RewriteLibrary`,
which keeps the package's own name and import path and gives it a hook; every
other package they import is copied without rewriting, re-emitted from its own
syntax tree, and only for the files that survive the build constraints.
_Avoid_: transform, compile, codegen

**Hook**:
The exported function a rewritten library gains, which runs that package's
initialization when a command that imports it is dispatched. It calls the
hooks of the package's direct imports first, then the package's own hoisted
variables and `init` bodies, and a generated package-level boolean makes the
second and later calls return immediately. Emitted as `BusyboxInit`, in a
generated `bbinit.go`. See [ADR 0004](docs/adr/0004-library-init-runs-for-the-command-that-needs-it.md).
_Avoid_: initializer, callback, trampoline, thunk

**Deferred**:
Said of a library whose initialization has moved into its hook, so that it
runs for the command that imports it rather than for every command in the
busybox. The complement of the eager set over the busybox's libraries: a
command is neither, its initialization already waiting for its own dispatch.
_Avoid_: lazy package, delayed, postponed

**Registered main**:
The function a command's `main` is renamed to, so that calling it becomes an
ordinary function call rather than program entry. Emitted as `registeredMain`.
_Avoid_: entrypoint, real main

**Registered init**:
The generated function that calls a command's extracted initializers in the
order Go would have run them. Emitted as `registeredInit`.
_Avoid_: setup, bootstrap

**Busybox init**:
One of the numbered functions the rewrite produces, emitted as `busyboxInitN`.
The first holds the calls to the hoisted global variable initializers, in init
order; each of the rest holds either the body of an original `init` or the
assignment hoisted out of one global variable. A command reaches them through
its registered init and a rewritten library through its hook.
_Avoid_: initN, init function

**Init order**:
The sequence in which a command's global variable initializers must run, taken
from the type checker rather than from source order. It is the go/types
`InitOrder`.
_Avoid_: dependency order

**Eager set**:
The packages of a busybox whose initialization runs at program start, before
any command is dispatched, rather than when a command that imports them is.
It is a closure, not a per-package decision: an eager package initializes
before every deferred one, so everything an eager package imports is eager
too. Standard library packages are always in it, as are packages carrying
`//go:linkname`, an assembly file or a `.syso` object, packages whose hoisted
variables would have a type they cannot name, and the packages a two-argument
`//go:linkname` names. A command is never in it, being rewritten whatever its
own source carries. Decided by `lazyinit.Plan`, which reports membership
through `Eager`, the reason through `EagerReason`, and why a named package
will not move through `CannotDefer`.
_Avoid_: eager flag, eager list, non-lazy set

**Registry**:
The runtime map from command name to its registered init and registered main.
Each rewritten command adds itself to the registry from a generated `init`. No
identifier for the concept: the map is `bbmain.bbCmds`, populated by
`bbmain.Register`.
_Avoid_: table, dispatch map

**bbmain**:
The package holding the registry, copied verbatim into the generated tree rather
than generated. It is deliberately dependency-free, so that importing it cannot
pull anything into every command.
_Avoid_: runtime, bblib

### The generated module

**Generated tree**:
The directory Go Busybox writes, containing the generated main program, bbmain,
and every rewritten command and copied dependency. It is a self-contained Go
module and can be built on its own. The field is `Opts.GenSrcDir`, set by
`makebb -gen-dir`.
_Avoid_: temp dir, output dir, scratch dir

**Busybox module**:
The module the generated tree declares, named so that no command compiled into
it is likely to share the path. Only an exact match breaks: the generated
`go.mod` would have to require the very module it declares. A module merely
sitting underneath the path resolves through the vendor manifest and is fine.
The constant is `bbModulePath`.
_Avoid_: bb module, fake module

**Vendor manifest**:
The generated list of which module each vendored package is attributed to,
written to `vendor/modules.txt`. It and the generated `go.mod` are written
together from one package list, because the go tool rejects a vendor directory
that disagrees with its `go.mod`. The function is `vendorManifest`.
_Avoid_: vendor list

**Synthetic version**:
The version recorded for a module that has none of its own, namely a main
module. A vendored build never resolves it, so it only has to be syntactically
valid and consistent. The function is `syntheticVersionFor`.
_Avoid_: dummy version, placeholder version

**Offline build**:
A compilation that reaches neither the network nor the module cache. Vendoring
everything into the generated tree is what preserves this property for inputs
that already had it.
_Avoid_: hermetic build, air-gapped build

### Finding commands

**Build environment**:
The Go toolchain settings used to load and compile packages: `GOOS`, `GOARCH`,
build tags, module mode, which compiler binary to run. The type is
`golang.Environ`, carried as `Opts.Env`.
_Avoid_: env, lookup environment

**Lookup environment**:
The settings used to turn user-supplied patterns into package paths, namely the
search path and the u-root source shortcut. It decides *which* commands are
built, not *how*. The type is `findpkg.Env`, carried as `Opts.LookupEnv`.
_Avoid_: env, build environment

**Pattern**:
One user-supplied argument naming commands. It may be a file system path, a Go
package path, a glob of either, or an exclusion.
_Avoid_: arg, target, spec

**Exclusion**:
A pattern prefixed with `-`, removing matching commands from the set the other
patterns produced. The functions are `splitExclusions` and `excludePaths`.
_Avoid_: negation, filter, ignore

**Search path**:
A list of directory prefixes that are each prepended to a pattern and tested,
so that commands from several repositories can be named without repeating their
common prefix. The field is `findpkg.Env.GBBPath`, set from `GBB_PATH`.
_Avoid_: include path

**U-root source shortcut**:
A local u-root checkout that `github.com/u-root/u-root/...` patterns resolve
against, ahead of the search path, instead of going through the module system.
The field is `findpkg.Env.URootSource`, set from `UROOT_SOURCE`.
_Avoid_: u-root path, source override

**Workspace mode**:
Using a Go workspace to span several modules, so that commands from different
modules can be compiled together. Since every command must belong to a module,
this is the only way to build across modules from local checkouts; see
`docs/adr/0002-gopath-mode-is-not-supported.md`. `goanywhere` generates the
workspace.
_Avoid_: multi-module mode
