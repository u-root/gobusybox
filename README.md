# Go Busybox

[![PkgGoDev](https://pkg.go.dev/badge/github.com/u-root/gobusybox/src)](https://pkg.go.dev/github.com/u-root/gobusybox/src)
[![Build Status](https://circleci.com/gh/u-root/gobusybox.svg?style=svg)](https://circleci.com/gh/u-root/gobusybox/tree/main)
[![Slack](https://slack.osfw.dev/badge.svg)](https://slack.osfw.dev)

**Contact**: best bet to reach us is the
[#u-root-dev](https://osfw.slack.com/messages/u-root-dev) channel on the [Open
Source Firmware Slack](https://slack.osfw.dev) ([Sign Up
Link](https://slack.osfw.dev)).

Go Busybox is a set of Go tools that allow you to compile many Go commands into
one binary. The resulting binary uses its invocation arguments (`os.Args`) to
determine which command is being called.

| Feature    | Support status                                        |
| ---------- | ----------------------------------------------------- |
| Go version | Tested are 1.20-1.22                                  |
| Packaging  | Go workspaces, Go modules, Go vendoring               |
| `GOOS`     | any (linux is tested)                                 |
| `GOARCH`   | any (amd64, arm, arm64, riscv64 are tested)           |
| CGO        | *Not supported*                                       |

An example:

```bash
go install github.com/u-root/gobusybox/cmd/makebb@latest

git clone github.com/u-root/u-root
cd u-root
makebb ./cmds/core/dmesg ./cmds/core/strace
```

A binary named `bb` should appear. It can be invoked in one of two ways --
either with a symlink or using a second argument.

```bash
./bb dmesg
./bb strace echo "hi"
```

It is meant to be used with symlinks for convenience:

```bash
# Make a symlink dmesg -> bb
ln -s bb dmesg
# Symlink means that argv[0] is the command name.
./dmesg

# Make a symlink strace -> bb
ln -s bb strace
./strace echo "hi"
```

Go Busybox does this by copying all the source for these Go commands and
rewriting it [in a temporary directory](#how-it-works).

Go Busybox can be used with **any Go commands** across multiple Go modules:

```sh
mkdir workspace
cd workspace

git clone https://github.com/hugelgupf/p9
git clone https://github.com/u-root/cpu

go work init ./p9
go work use ./cpu

makebb ./cpu/cmds/* ./p9/cmd/*
```

> [!IMPORTANT]
> `makebb` works any time `go build` or `go list` also work.
>
> For multi-module compilation, use Go workspaces or read below.

## Path resolution & multi-module builds

`makebb` and the APIs mentioned below all accept commands from file system
paths, Go package paths, and globs matching path.Match thereof.

### makebb with globs and exclusions

In addition to the standard syntaxes supported by `go list` and `go build`,
`makebb` also accepts globs and exclusions.

```sh
git clone https://github.com/u-root/u-root
cd u-root

makebb ./cmds/core/ip ./cmds/core/init

# Escaping the * to show that the Go APIs know how to resolve this as well
makebb ./cmds/core/\*
makebb ./cmds/core/i\* ./cmds/boot/pxeboot

# All core commands except ip.
makebb ./cmds/core/\* -./cmds/core/ip
```

### makebb with Go workspaces & `GBB_PATH`.

To compile commands from multiple modules, you may use workspaces.

```shell
mkdir workspace
cd workspace

git clone https://github.com/u-root/u-root
git clone https://github.com/u-root/cpu

go work init ./u-root
go work use ./cpu

makebb \
    ./u-root/cmds/core/init \
    ./u-root/cmds/core/elvish \
    ./cpu/cmds/cpud

# Also works for offline builds with `go work vendor` (Go 1.22 feature):
go work vendor

makebb \
    ./u-root/cmds/core/init \
    ./u-root/cmds/core/elvish \
    ./cpu/cmds/cpud
```

For a shortcut to specify many commands sharing common path elements (e.g. from
the same repository), the `GBB_PATH` environment variable exists. Paths are
concatenated with every colon-separated element of `GBB_PATH` from left to right
and checked for existence.

```shell
GBB_PATH=$(pwd)/u-root:$(pwd)/cpu makebb \
    cmds/core/init \
    cmds/core/elvish \
    cmds/cpud

# matches:
#   $(pwd)/u-root/cmds/core/init
#   $(pwd)/u-root/cmds/core/elvish
#   $(pwd)/cpu/cmds/cpud
```

### goanywhere

```shell
go install github.com/u-root/gobusybox/src/cmd/goanywhere@latest
```

`goanywhere` creates a Go workspace temporarily on the fly from the packages'
modules in the given local file paths.

goanywhere then executes the command given after "--" in the workspace
directory and amends the packages as args.

For example,

```
# -o $(pwd) is needed since goanywhere executes the command in the workspace
# directory, and by default the Go binary is created in the current wd.
goanywhere ./u-root/cmds/core/{init,gosh} -- go build -o $(pwd)
goanywhere ./u-root/cmds/core/{init,gosh} -- makebb -o $(pwd)
goanywhere ./u-root/cmds/core/{init,gosh} -- mkuimage [other args]
goanywhere ./u-root/cmds/core/{init,gosh} -- u-root [other args]
```

Like `makebb`, `goanywhere` supports `GBB_PATH`, globs and shell expansions.

```
GBB_PATH=$(pwd)/u-root:$(pwd)/cpu goanywhere \
    cmds/core/{init,gosh} cmds/cpud -- makebb -o $(pwd)
```

### makebb with multiple Go modules

`makebb` supports Go workspaces for locally checked out sources, as shown above.

For multiple Go module command dependencies that aren't checked out locally, we
apply some Go module tricks.

To depend on commands outside of ones own repository, the easiest way to depend
on Go commands is the following:

```sh
TMPDIR=$(mktemp -d)
cd $TMPDIR
go mod init foobar
```

Create a file with some unused build tag like this to create dependencies on commands:

```go
//go:build tools

package something

import (
        _ "github.com/u-root/u-root/cmds/core/ip"
        _ "github.com/u-root/u-root/cmds/core/init"
        _ "github.com/hugelgupf/p9/cmd/p9ufs"
)
```

You can generate this file for your repo with the `gencmddeps` tool:

```
go install github.com/u-root/gobusybox/src/cmd/gencmddeps@latest

gencmddeps -o deps.go -t tools -p something \
    github.com/u-root/u-root/cmds/core/{ip,init} \
    github.com/hugelgupf/p9/cmd/p9ufs
```

> [!IMPORTANT]
> `gencmddeps` does not support file paths or exclusions, as these rely on a
> `go.mod` already being present to resolve the Go package name.
>
> The input to gencmddeps must be full Go package paths.

The unused build tag keeps it from being compiled, but its existence forces `go
mod` to add these dependencies to `go.mod`:

```sh
go mod tidy

makebb \
  github.com/u-root/u-root/cmds/core/ip \
  github.com/u-root/u-root/cmds/core/init \
  github.com/hugelgupf/p9/cmd/p9ufs

# Also works with vendored, offline builds:
go mod vendor

makebb \
  github.com/u-root/u-root/cmds/core/ip \
  github.com/u-root/u-root/cmds/core/init \
  github.com/hugelgupf/p9/cmd/p9ufs
```

## APIs

Besides the makebb CLI command, there is a
[Go API at src/pkg/bb](https://pkg.go.dev/github.com/u-root/gobusybox/src/pkg/bb).

## Shortcomings

-   Any *imported* packages' `init` functions are run for *every* command.

    For example, if some command imports and uses the `testing` package, all
    commands in the busybox will have testing's flags registered as a side
    effect, because `testing`'s init function runs with every command.

    While Go busybox handles every main commands' init functions, it does not
    handle dependencies' init functions. Done properly, it would rewrite all
    non-standard-library packages as well as commands. This has not been
    necessary to implement so far. It would likely be necessary if, for example,
    two different imported packages register the same flag unconditionally
    globally.

## How It Works

[src/pkg/bb](src/pkg/bb) implements a Go source-to-source transformation on pure
Go code (no cgo).

This AST transformation does the following:

-   Takes a Go command's source files and rewrites them into Go package files
    (almost) without global side effects.
-   Writes a `main.go` file with a `main()` that calls into the appropriate Go
    command package based on `argv[0]` or `argv[1]`.

This allows you to take two Go commands, such as Go implementations of `dmesg`
and `strace` and compile them into one binary.

Which command is invoked is determined by `argv[0]` or `argv[1]` if `argv[0]` is
not recognized. Let's say `bb` is the compiled binary; the following are
equivalent invocations of `dmesg` and `strace`:

```sh
(cd ./src/cmd/makebb && go install)
(cd ./test/nested && makebb ./cmd/dmesg ./cmd/strace)

# Make a symlink dmesg -> bb
ln -s bb dmesg
./dmesg

# Make a symlink strace -> bb
ln -s bb strace
./strace echo "hi"
```

```sh
./bb dmesg
./bb strace echo "hi"
```

### Command Transformation

Principally, the AST transformation moves all global side-effects into callable
package functions. E.g. `main` becomes `registeredMain`, each `init` becomes
`initN`, and global variable assignments are moved into their own `initN`. A
`registeredInit` calls each `initN` function in the correct init order.

Then, these `registeredMain` and `registeredInit` functions can be registered
with a global map of commands by name and used when called upon.

Let's say a command `github.com/org/repo/cmds/sl` contains the following
`main.go`:

```go
package main

import (
  "flag"
  "log"
)

var name = flag.String("name", "", "Gimme name")

func init() {
  log.Printf("init %s", *name)
}

func main() {
  log.Printf("train")
}
```

This would be rewritten to be:

```go
package sl // based on the directory name

import (
  "flag"
  "log"

  "../bb/pkg/bbmain" // generated import path
)

// Type has to be inferred through type checking.
var name *string

func init0() {
  log.Printf("init %s", *name)
}

func init1() {
  name = flag.String("name", "", "Gimme name")
}

func registeredInit() {
  // Order is determined by go/types.Info.InitOrder.
  init1()
  init0()
}

func registeredMain() {
  log.Printf("train")
}

func init() {
  bbmain.Register("sl", registeredInit, registeredMain)
}
```

### Generated main.go

The main.go file is generated from
[./src/pkg/bb/bbmain/cmd/main.go](./src/pkg/bb/bbmain/cmd/main.go).

```go
package main

import (
  "os"
  "log"
  "path/filepath"

  // Side-effect import so init in sl calls bbmain.Register
  _ "github.com/org/repo/cmds/sl"

  "../bb/pkg/bbmain"
)

func main() {
  bbmain.Run(filepath.Base(os.Argv[0]))
}
```

### Directory Structure

All files are written into a temporary directory. All dependency Go packages are
also written there.

The directory structure we generate resembles a $GOPATH-based source tree, even
if we are combining module-based Go commands. Regardless of whether the original
commands are based on Go modules, Go workspaces, or GOPATH, we generate the same
structure and compiled with `GOPATH=$tmpdir GO111MODULE=off`.

This means that in all cases, traditionally offline compilations remain offline
(e.g. GOPATH, or vendored modules / workspaces).

```
/tmp/bb-$NUM/
└── src
    ├── bb.u-root.com
    │   └── bb
    │       ├── main.go               << ./src/pkg/bb/bbmain/cmd/main.go (with edits)
    │       └── pkg
    │           └── bbmain
    │               └── register.go   << ./src/pkg/bb/bbmain/register.go
    └── github.com
        └── u-root
            ├── uio
            │   ├── uio               << dependency used by both
            │   └── ulog              << dependency used by both
            ├── u-bmc
            │   ├── cmd
            │   │   ├── fan           << generated command package
            │   │   ├── login         << generated command package
            │   │   └── socreset      << generated command package
            │   └── pkg
            │       ├── acme          << dependency copied from u-bmc
            │       ├── aspeed        << dependency copied from u-bmc
            │       ├── gpiowatcher   << dependency copied from u-bmc
            │       └── mtd           << dependency copied from u-bmc
            └── u-root
                ├── cmds
                │   └── core
                │       ├── cat       << generated command package
                │       ├── ip        << generated command package
                │       └── ls        << generated command package
                └── pkg
                    ├── curl          << dependency copied from u-root
                    ├── dhclient      << dependency copied from u-root
                    ├── ip            << dependency copied from u-root
                    ├── ls            << dependency copied from u-root
                    └── uio           << dependency copied from u-root
```
