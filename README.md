## Go Busybox

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

### AST Transformation

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

  // This package holds the global map of commands.
  "github.com/u-root/u-root/pkg/bb"
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

#### Shortcomings

-   If there is already a function `Main` or `InitN` for some `N`, there may be
    a compilation error.
-   Any packages imported by commands may still have global side-effects
    affecting other commands. Done properly, we would have to rewrite all
    non-standard-library packages as well as commands. This has not been
    necessary to implement so far. It would likely be necessary if two different
    imported packages register the same flag unconditionally globally.

## Generated main

The main file can be generated based on any template Go files, but the default
looks something like the following:

```go
import (
  "os"

  "github.com/u-root/u-root/pkg/bb"

  mangledsl "github.com/org/repo/cmds/generated/sl"
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

The default template will use `argv[1]` if `argv[0]` is not in the map.
