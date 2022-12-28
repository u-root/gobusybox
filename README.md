## Go Busybox

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
| Go version | Tested are 1.15-1.19                                  |
| Packaging  | Go modules, Go vendoring, bazel w/ [rules_go](https\://github.com/bazelbuild/rules_go) |
| `GOOS`     | linux (others may work, but untested)                 |
| `GOARCH`   | amd64, arm, arm64, riscv64 (others may work, but untested) |
| CGO        | *Not supported*                                       |

Other `GOARCH` and `GOOS` architectures are likely to work as well, but are
untested.

An example:

```bash
(cd ./src/cmd/makebb && go install)
makebb ./test/nested/cmd/dmesg ./test/nested/cmd/strace
```

A binary named `bb` should appear. It can be invoked in one of two ways --
either with a symlink or using a second argument.

```bash
# Make a symlink dmesg -> bb
ln -s bb dmesg
# Symlink means that argv[0] is the command name.
./dmesg

# Make a symlink strace -> bb
ln -s bb strace
./strace echo "hi"
```

If symlinks are a hassle, you can also invoke the binary like this:

```bash
./bb dmesg
./bb strace echo "hi"
```

Go Busybox does this by copying all the source for these Go commands and
rewriting it [in a temporary directory](#how-it-works).

Go Busybox can be used with **any Go commands** across multiple Go modules:

```sh
git clone https://github.com/hugelgupf/p9
git clone https://github.com/gokrazy/gokrazy

makebb ./p9/cmd/* ./gokrazy/cmd/*
```

For the moment, `makebb` is tested with repositories on the local file system.
Using Go import paths is supported, as well, but not as well-tested.

### APIs

Besides the makebb CLI command, there is a
[Go API at src/pkg/bb](https://pkg.go.dev/github.com/u-root/gobusybox/src/pkg/bb)
and bazel rules in [src/gobb2.bzl](src/gobb2.bzl).

#### Using bazel go_busybox rule

Assuming you have [rules_go](https://github.com/bazelbuild/rules_go) set up, add
the following to your `WORKSPACE`:

```bzl
git_repository(
    name = "com_github_u_root_gobusybox",

    # We do not have regular releases yet.
    #
    # We also do not guarantee compatibility yet, so it may be worth choosing a
    # commit and setting `commit = "hash"` here instead of the branch.
    branch = "main",
    remote = "https://github.com/u-root/gobusybox.git",
)
```

Then, in any `BUILD` file, you can create a busybox like this:

```bzl
load("@com_github_u_root_gobusybox//src:gobb2.bzl", "go_busybox")

go_busybox(
    name = "bb",
    cmds = [
        # These must be absolute labels, for the moment, and each command must
        # be listed individually. (No :... or :all target patterns.)
        "//cmd/foobar",
        "//cmd/otherbar",

        # Another repository's go_binarys are totally fine, e.g. if imported
        # with gazelle's go_repository rule.
        "@com_github_u-root_u-root//cmds/core/ls",
    ],
)
```

For the moment, the targets listed on `cmds` must be **individual, absolute
labels** (issue [#38](https://github.com/u-root/gobusybox/issues/38)).

### Shortcomings

-   Any *imported* packages' `init` functions are run for *every* command.

    For example, if some command imports the `testing` package, all commands in
    the busybox will have testing's flags registered as a side effect, because
    `testing`'s init function runs with every command.

    While Go busybox handles every main commands' init functions, it does not
    handle dependencies' init functions. Done properly, it would have to rewrite
    all non-standard-library packages as well as commands. This has not been
    necessary to implement so far. It would likely be necessary if, for example,
    two different imported packages register the same flag unconditionally
    globally.

-   There are still some issues with Go module dependency resolution. Please
    file an [issue](https://github.com/u-root/gobusybox/issues/new) if you
    encounter one, even if it turns out to be your own issue -- our error
    messages should be telling users what to fix and why.

## Common Dependency Conflicts

If commands from more than one Go module are combined into a busybox, there are
a few common dependency pitfalls to be aware of. Go busybox will do its best to
log actionable suggestions in case of conflicts.

It's important to be aware that not all `go.mod` files are equal. The
[**main module**](https://golang.org/ref/mod) is the module containing the
directory where the `go` command is invoked. `replace` and `exclude` directives
only apply in the main module's `go.mod` file and are ignored in other modules.

If Go busybox is asked to combine programs under different main modules, it will
do its best to merge the `replace` and `exclude` directives from all main module
`go.mod` files.

Let's say, for example, that [u-root](https://github.com/u-root/u-root)'s
`cmds/core/*` is being combined into a busybox with
[u-bmc](https://github.com/u-root/u-bmc)'s `cmd/*`. Each have a main module
`go.mod`, one at `u-root/go.mod` and one at `u-bmc/go.mod`.

```
$ cat ./u-root/go.mod
...
replace github.com/intel-go/cpuid => /somewhere/cpuid
exclude github.com/insomniacslk/dhcp v1.0.2
```

```
$ cat ./u-bmc/go.mod
...
replace github.com/intel-go/cpuid => /somewhere/cpuid
exclude github.com/mdlayher/ethernet v1.0.3
```

Go busybox generated `go.mod` (does not list `require` statements):

```
...
# Because *both* u-root/go.mod and u-bmc/go.mod pointed to a local copy of cpuid
replace github.com/intel-go/cpuid => ./src/github.com/intel-go/cpuid

# From either go.mod file.
exclude github.com/insomniacslk/dhcp v1.0.2
exclude github.com/mdlayher/ethernet v1.0.3
```

Certain conflicts can come up during this process. This section covers each
potential conflict and potential solutions you can enact in your code:

1.  Conflicting local commands. E.g. two local copies of `u-root` and `u-bmc`
    are being combined into a busybox with `makebb ./u-root/cmds/core/*
    ./u-bmc/cmd/*`. If `u-bmc/go.mod` depends on u-root@v3 from GitHub, that
    conflicts with the local `./u-root` being requested with makebb. Gobusybox
    will select the localversion of u-root over the one from GitHub. If you
    want that gobusybox does **not** do this, you can set the environment
    variable `GBB_STRICT=1` to run gobusybox in strict mode. If gobusybox
    runs in strict mode, it will fail.

    **Solution**: `u-bmc/go.mod` needs `replace github.com/u-root/u-root =>
    ../u-root`.

1.  Conflicting local `replace` directives. Ex:

    ```
    > u-root/go.mod
    replace github.com/insomniacslk/dhcp => ../local/dhcp

    > u-bmc/go.mod
    require github.com/insomniacslk/dhcp v1.0.2
    ```

    u-root has replaced dhcp, but u-bmc still depends on the remote dhcp@v1.0.2.

    **Solution**: u-root drops local replace rule, or `u-bmc/go.mod` needs
    `replace github.com/insomniacslk/dhcp => $samedir/local/dhcp` as well.

1.  Two conflicting local `replace` directives. Ex:

    ```
    > u-root/go.mod
    replace github.com/insomniacslk/dhcp => /some/dhcp

    > u-bmc/go.mod
    replace github.com/insomniacslk/dhcp => /other/dhcp
    ```

    **Solution**: both go.mod files must point `replace
    github.com/insomniacslk/dhcp` at the same directory.

## How It Works

[src/pkg/bb](src/pkg/bb) implements a Go source-to-source transformation on pure
Go code (no cgo).

This AST transformation does the following:

-   Takes a Go command's source files and rewrites them into Go package files
    (almost) without global side effects.
-   Writes a `main.go` file with a `main()` that calls into the appropriate Go
    command package based on `argv[0]` or `argv[1]`.

This allows you to take two Go commands, such as Go implementations of `sl` and
`cowsay` and compile them into one binary.

Which command is invoked is determined by `argv[0]` or `argv[1]` if `argv[0]` is
not recognized. Let's say `bb` is the compiled binary; the following are
equivalent invocations of `sl` and `cowsay`:

```sh
(cd ./src/cmd/makebb && go install)
makebb ./test/nested/cmd/dmesg ./test/nested/cmd/strace

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
package sl // based on the directory name or bazel-rule go_binary name

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

All files are written into a temporary directory. All dependencies that can be
found on the local file system are also written there.

The directory structure we generate resembles a $GOPATH-based source tree, even
if we are combining module-based Go commands. This just lends itself to code
reuse within bb: if you remove all the go.mod file, and add in vendored files,
the tree still compiles.

```
/tmp/bb-$NUM/
└── src
    ├── bb.u-root.com
    │   └── bb
    │       ├── go.mod                << generated main module go.mod (see below)
    │       ├── go.sum                << generated main module go.sum (concat of u-bmc/go.sum and u-root/go.sum)
    │       ├── main.go               << ./src/pkg/bb/bbmain/cmd/main.go (with edits)
    │       └── pkg
    │           └── bbmain
    │               └── register.go   << ./src/pkg/bb/bbmain/register.go
    └── github.com
        └── u-root
            ├── u-bmc
            │   ├── cmd
            │   │   ├── fan           << generated command package
            │   │   ├── login         << generated command package
            │   │   └── socreset      << generated command package
            │   ├── go.mod            << copied from u-bmc (if module)
            │   ├── go.sum            << copied from u-bmc (if module)
            │   └── pkg
            │       ├── acme          << local dependency copied from u-bmc
            │       ├── aspeed        << local dependency copied from u-bmc
            │       ├── gpiowatcher   << local dependency copied from u-bmc
            │       └── mtd           << local dependency copied from u-bmc
            └── u-root
                ├── cmds
                │   └── core
                │       ├── cat       << generated command package
                │       ├── ip        << generated command package
                │       └── ls        << generated command package
                ├── go.mod            << copied from u-root (if module)
                ├── go.sum            << copied from u-root (if module)
                └── pkg
                    ├── curl          << local dependency copied from u-root
                    ├── dhclient      << local dependency copied from u-root
                    ├── ip            << local dependency copied from u-root
                    ├── ls            << local dependency copied from u-root
                    └── uio           << local dependency copied from u-root
```

#### Dependency Resolution

There are two kinds of dependencies we care about: remote go.mod dependencies,
and local file system dependencies.

For remote go.mod dependencies, we copy over all go.mod files into the
transformed dependency tree. (See u-root/go.mod and u-bmc/go.mod in the example
above.)

Local dependencies can be many kinds, and they all need some special attention:

-   non-module builds: dependencies in $GOPATH need to either be copied into the
    new tree, or we need to set our `GOPATH=/tmp/bb-$NUM:$GOPATH` to find these
    dependencies.
-   non-module builds: dependencies in vendor/ need to be copied into the new
    tree.
-   module builds: dependencies within a command's own module (e.g.
    u-root/cmds/core/ls depends on u-root/pkg/ls) need to be copied into the new
    tree.
-   module builds: `replace`d modules on the local file system. `replace`
    directives are only respected in
    [main module go.mod](https://golang.org/ref/mod) files, which would be
    `u-root/go.mod` and `u-bmc/go.mod` respectively in the above example. The
    compiled busybox shall respect **all** main modules' `replace` directives,
    so they must be added to the generated main module go.mod.

### Generated main module go.mod & go.sum

The generated main module go.mod refers packages to their local copies:

```
package bb.u-root.com # some domain that will never exist

# As of Go 1.16 these are required, even for local-only modules.
#
# We fill in the real version number if we know, otherwise v0.0.0.
require github.com/u-root/u-root vN.N.N
require github.com/u-root/u-bmc vN.N.N

replace github.com/u-root/u-root => ./src/github.com/u-root/u-root
replace github.com/u-root/u-bmc => ./src/github.com/u-root/u-bmc

# also, this must have copies of `replace` and `exclude` directives from
# u-root/go.mod and u-bmc/go.mod
#
# if these fundamentally conflict, we cannot build a unified busybox.
```

If `u-root/go.mod` and `u-bmc/go.mod` contained any `replace` or `exclude`
directives, they also need to be placed in this go.mod, which is the main module
go.mod for `bb/main.go`.

The generated `go.sum` will be a concatenation of `u-root/go.sum` and
`u-bmc/go.sum`.
