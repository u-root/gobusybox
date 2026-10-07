# The generated tree is a vendored Go module, not a GOPATH tree

Go Busybox used to write its generated sources into a `$GOPATH`-shaped tree and
compile them with `GOPATH=$tmpdir GO111MODULE=off`, because disabling modules
was the simplest way to guarantee that a build which started offline stayed
offline. We now generate a single Go module instead, with every rewritten
command and every non-standard-library dependency vendored into it at its
original import path, built with `GOWORK=off go build -mod=vendor` — which is
equally offline, since `-mod=vendor` consults neither the network nor the module
cache, but does not depend on a mode the Go toolchain has been retiring.

## Consequences

- `go.mod` and `vendor/modules.txt` must be generated together from one package
  list. The go tool rejects a vendor directory whose manifest disagrees with its
  `go.mod`, so the two can never be derived independently.

- Packages with no module of their own — anything found in GOPATH mode — are
  each recorded as a synthetic single-package module at a synthetic version.
  Nothing ever resolves those versions, so they only have to be syntactically
  valid and internally consistent.

- Packages reached through a GOPATH-style nested `vendor/` directory are
  reported by `go/packages` under their vendored path but imported under their
  real one. A module vendor directory is flat, so that prefix is stripped when
  writing them.

- Unlike `GO111MODULE=off`, a module build enforces `go` directives. The
  generated module therefore declares a version at least as high as every module
  it vendors, the way `go mod tidy` would; otherwise the build fails before
  `GOTOOLCHAIN` gets the chance to select a new enough toolchain.
