## Go Busybox

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
go build src/cmd/makebb
./makebb modtest/cmd/dmesg modtest/cmd/strace

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
package functions. E.g. `main` becomes `Main`, each `init` becomes `InitN`, and
global variable assignments are moved into their own `InitN`.

Then, these `Main` and `Init` functions can be registered with a global map of
commands by name and used when called upon.

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
)

// Type has to be inferred through type checking.
var name *string

func Init0() {
  log.Printf("init %s", *name)
}

func Init1() {
  name = flag.String("name", "", "Gimme name")
}

func Init() {
  // Order is determined by go/types.Info.InitOrder.
  Init1()
  Init0()
}

func Main() {
  log.Printf("train")
}
```

### Generated main.go

```go
package main

import (
  "os"

  mangledsl "github.com/org/repo/cmds/sl"
)

var bbcmds = map[string]...

func Register(...)

func Run(name string) {
  if funcs, ok := bbcmds[name]; ok {
    funcs.Init()
    funcs.Main()
    os.Exit(0)
  }
}

func main() {
  Run(os.Argv[0])
}

func init() {
  Register("sl", mangledsl.Init, mangledsl.Main)
}
```

### Directory Structure

All files are written into a temporary directory. All dependencies that can be
found on the local file system are also written there.

The directory structure we generate resembles a $GOPATH-based source tree, even
if we are combining module-based Go commands. This just lends code reuse within
bb: if you remove all the go.mod file, and add in vendored files, the tree still
compiles.

```
/tmp/bb-$NUM/
├── go.mod                            << generated top-level go.mod (see below)
└── src
    ├── bb
    │   └── main.go                   << generated main.go
    └── github.com
        └── u-root
            ├── u-bmc
            │   ├── cmd
            │   │   ├── fan           << generated command package
            │   │   ├── login         << generated command package
            │   │   └── socreset      << generated command package
            │   ├── go.mod            << remote dependency manifest copied from u-bmc (if module)
            │   └── pkg
            │       ├── acme          << local dependency copied from u-bmc
            │       ├── aspeed        << local dependency copied from u-bmc
            │       ├── gpiowatcher   << local dependency copied from u-bmc
            │       └── mtd           << local dependency copied from u-bmc
            └── u-root
                ├── cmds
                │   └── core          << generated command package
                │       ├── cat       << generated command package
                │       ├── ip        << generated command package
                │       └── ls        << generated command package
                ├── go.mod            << remote dependency manifest copied from u-root (if module)
                └── pkg
                    ├── curl          << local dependency copied from u-root
                    ├── dhclient      << local dependency copied from u-root
                    ├── ip            << local dependency copied from u-root
                    ├── ls            << local dependency copied from u-root
                    └── uio           << local dependency copied from u-root
```

### Top-level go.mod

The top-level go.mod refers packages to their local copies:

```
package bb.u-root.com # some domain that will never exist

replace github.com/u-root/u-root => ./src/github.com/u-root/u-root
replace github.com/u-root/u-bmc => ./src/github.com/u-root/u-bmc
```

### Shortcomings

-   If there is already a function `Main` or `InitN` for some `N`, there may be
    a compilation error.
-   Any packages imported by commands may still have global side-effects
    affecting other commands. Done properly, we would have to rewrite all
    non-standard-library packages as well as commands. This has not been
    necessary to implement so far. It would likely be necessary if two different
    imported packages register the same flag unconditionally globally.
