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

- Packages with no module of their own are not representable: a vendored
  package is recorded under its module's path and version, so every package
  needs a module. This is what made GOPATH mode untenable; see
  [0002](0002-gopath-mode-is-not-supported.md). A main module has no version of
  its own, so its packages get a synthetic one. Nothing ever resolves these
  versions, so they only have to be syntactically valid and internally
  consistent.

- Unlike `GO111MODULE=off`, a module build enforces `go` directives. The
  generated module therefore declares a version at least as high as every module
  it vendors, the way `go mod tidy` would; otherwise the build fails before
  `GOTOOLCHAIN` gets the chance to select a new enough toolchain.
