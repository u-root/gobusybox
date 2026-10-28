# A library's init runs for the command that needs it

Go Busybox rewrites commands and copies the packages they import verbatim. A
copied package keeps its `init` and its initialised package-level variables, so
the go tool runs them at program start, for every command in the busybox,
whether or not the command being dispatched imports that package. Only the
command's own initializers are deferred, by the rewrite that produces
`registeredInit`.

That is wasted work in proportion to how little the busybox's packages are
shared, and they are shared far less than one might guess. In the 120-command
u-root busybox, of 228 library packages, **none is reached by every command**,
one is reached by more than half, and **125 (55%) are reached by exactly one
command**. The cost is paid by all 120.

It is worst when a heavy package enters the busybox. Adding a single
grpc-serving command to a busybox holding three commands that print a string
changes what one of those three does before reaching its own `main`:

| running a `fmt.Println` command | without the grpc command | with it |
|---|---|---|
| packages running `init` | 26 | 183 |
| `init` clock | 0.10 ms | 1.12 ms |
| allocated during `init` | 2.9 KB | 368 KB |

Go Busybox will therefore rewrite library packages as well as commands, so that
a library's `init` becomes an ordinary function call that the dispatched
command makes, and a command initialises nothing it does not import.

## How

Each rewritten library gains one exported hook, named through the same
collision check that picks `registeredMain`. The hook calls the hooks of the
package's **direct imports** first, then runs that package's own
initialization, and a generated package-level boolean makes the second and
later calls return immediately. The generated tree writes a `bbinit.go` beside
each rewritten command calling the hooks of the command's own direct imports,
and the recursion reaches the rest.

It has to be direct imports rather than a flat list of the whole graph,
because `internal/` visibility is enforced inside `vendor/` as everywhere
else. A command calling every package in its graph would have to import every
package in its graph, and fails as soon as one of them is another module's
`internal/` package:

```
vendor/example.com/other/other.go:6:2: use of internal package
example.com/lib/internal/hidden not allowed
```

That is not an exotic case. `google.golang.org/grpc`,
`google.golang.org/protobuf` and `golang.org/x/net` all keep most of
themselves under `internal/`. Calling only direct imports is legal by
construction, since a package may always import what it already imports.

The order this produces is a depth-first walk, with each package's direct
imports taken in import path order so that two builds of one input agree. It
puts every package after all of its imports.

That is not the order Go itself would have used, and the hooks cannot be made
to produce it. The specification fixes a total order rather than merely a
partial one: given the list of all packages sorted by import path, each step
initializes the first uninitialized package whose imports are all initialized
already. Because a hook calls only its own direct imports' hooks, a package's
deferred imports are always initialized contiguously with it, while Go's rule
interleaves them. The smallest graph that tells the two apart:

```
cmd -> a -> c         Go:        c d b e a
         -> e         the hooks: c e a d b   or   d b c e a
    -> b -> d
```

`d` and `b` fall inside `a`'s subtree, so no ordering of the command's own
hook calls reproduces Go's, and reordering the walk on its own would only
make it stop describing the generated code. Both orders put every package
after all of its imports, which is what a program can rely on without
declaring the dependency by importing it. The difference is recorded under
Consequences, because it is observable.

A package is deferred **entirely or not at all**: its hoisted package-level
variable initializers and its `init` bodies move into the hook together, in
the order Go would have run them. Deferring one without the other is not a
smaller step, it is a broken one. Go finishes all of an imported package's
initialization before the importing package's variables are initialized, so
leaving `var x = q.Lookup()` eager while deferring `q`'s `init` runs the
variable against a `q` that has not been initialized:

```
Go as it is:                      p.V = v
init deferred, variable eager:    p.V = <TABLE NOT BUILT>
```

This is also what `bbinternal.Package.Rewrite` already does for commands,
where `busyboxInit0` holds the hoisted variables and `busyboxInit1..N` the
`init` bodies, all reached through one `registeredInit`.

A package that is not rewritten keeps its `init` at program start, which
happens before any command is dispatched and therefore before any deferred
`init`. So a package that stays eager may not import one that does not: Go
would run the import first, and this arrangement would run it second, against
state the eager `init` has already read. **The set of packages left eager is
therefore closed under imports**, and is computed as a closure rather than
decided one package at a time. Standard library packages are never rewritten
and are always eager, which is consistent because no standard library package
imports a vendored one.

Laziness is refused outright for any package carrying `//go:linkname`, an
assembly file, or a `.syso` object. Each can bind a symbol in another package
by name, so such a package can be rewritten cleanly, compile, and still
misbehave, because something reaches its state by a path that is not in the
import graph. A `.syso` is the same hazard as assembly with none of the source
to read. The refusal is static and over-broad on purpose. Anything else that
defeats the rewrite falls back to eager and is reported at the end of the
build, never silently.

`.S` and `.sx` are assembly too and are deliberately absent from that list.
go/packages reports them only for a package that uses cgo, which this never
sees, because makebb disables cgo and gomodbb inherits that; measured on
Go 1.27 with `CGO_ENABLED=1`, a package carrying `empty.S` and `empty.sx`
reports no other files for either. Listing them would be a check that cannot
fire rather than a guard.

A fourth refusal is not about reaching a package from outside but about
writing it down. Hoisting `var V = expr` splits it into a declaration and an
assignment, and the declaration has to state the type that was inferred. When
that type is unexported and belongs to another package, there is no way to
state it:

```
package other                      package lib
type client struct{ ... }          var V = other.New()
func New() *client                 // rewritten as: var V *other.client
```

which the compiler refuses with *name client not exported by package other*.
Go has no declaration that names a type without naming it, so the choice is
between leaving the package's initialization where it is and emitting source
that does not build. Such a package is refused.

This one is not a consequence of deferring. `bbinternal.Package.Rewrite` has
hoisted a command's variables the same way since the beginning and fails the
same way on the same input; what changes is the chance of meeting it, because
commands are hand-written and libraries are whatever the module graph drags
in. The refusal covers the libraries this decision newly rewrites. The command
case is older and still open.

**A command is never refused**, whatever its own source carries. A refusal
makes a package an eager root, and an eager root drags everything it imports
eager with it, which is sound only for a package that initializes at program
start. A command does not: it is rewritten whatever this decides, its `main`
becoming `registeredMain` and its initialization `registeredInit`, called at
dispatch. Treating one as a root cost the entire busybox, since every library
is reached by some command, so a single command carrying an assembly file
would leave nothing deferrable. Its `//go:linkname` *targets* still join the
roots, by the rule below: those name a package the command may never import,
which nothing else would initialize.

Refusing the package that carries the directive is not quite enough. The
two-argument form, `//go:linkname local importpath.name`, names a symbol in
another package, and that is exactly an edge the import graph does not have,
so the closure above cannot reach it: the refused package becomes eager while
its target stays lazy, and the eager one reads the target's state before the
target has been initialized. **The target package of a two-argument
`//go:linkname` therefore joins the eager roots**, and the closure runs from
there as usual.

That is completeness rather than a repair. Scanning a grpc-serving command's
101 non-standard packages finds three two-argument pulls, all of them in
`golang.org/x/sys/unix`: `runtime.getAuxv`, `syscall.prlimit` and
`runtime.vgetrandom`. Every one of those targets is a standard library
package and so eager already, and every other such pull in the build is in
the standard library reaching the runtime. The count is per build rather than
per module, because the scanner reads the directives go/packages has already
filtered by build constraints; this one is linux/amd64 against x/sys v0.38.0.
The rule costs nothing to state and the scanner has to look at the directive
anyway.

Which package a symbol names is decided by which packages are in the build
rather than by the text, because both halves may contain dots:
`example.com/victim.(*T).Ready` is a method, so its final dot separates
nothing useful. Each dot is tried from the right and the longest prefix that
is a package here wins, `gopkg.in/yaml.v3` being the ordinary case and
`gopkg.in/yaml` possibly sitting beside it. When the package a symbol really
names is *not* in this build and a shorter prefix is, that shorter one is
taken and made eager. The answer is wrong and the direction is safe -- a
package is made eager that need not be -- and there is nothing to resolve it
with, since the package that would settle it is the one that is absent. It is
pinned by a test rather than fixed.

Importing `unsafe` is **not** on that list, though it is the obvious third
candidate. `unsafe` lets a package do what it likes to memory it already has;
it does not let another package reach this one without importing it, which is
the property the import graph has to carry. Refusing it would also cost
something, because the eager set is closed downward: for a single
grpc-serving command, of 101 non-standard packages,

| refused | eager after closure | `init` heap still recoverable |
|---|---|---|
| `//go:linkname`, assembly | **1** | 379 KB, 84% |
| and `unsafe` as well | **23** | 343 KB, 76% |

The count is the dramatic number and the heap is the honest one. Refusing
`unsafe` would make 22 more packages eager but cost only 36 KB of the
recoverable heap, because the packages it drags in are small:
`protobuf/internal/impl` allocates 288 bytes during `init`, and most of the
loss is one package, `protobuf/encoding/protowire`, at 33 KB. So this is a
decision worth 8 points of the heap, taken because the rule is wrong rather
than because it is expensive.

## What this actually recovers

For that same grpc-serving command, 141 packages run `init`, allocating
450 KB in 1.18 ms, and they divide as:

| | packages | `init` heap | share |
|---|---|---|---|
| deferrable | 53 | 379 KB | **84%** |
| refused, so eager | 1 | 48 B | 0% |
| standard library, always eager | 87 | 71 KB | 16% |

So grpc itself is deferrable in full: all 54 `google.golang.org/grpc`
packages and all 33 `google.golang.org/protobuf` packages come out lazy, and
the only non-standard package the rules refuse is `golang.org/x/sys/unix`,
which has both a `//go:linkname` and an assembly file and allocates 48 bytes.
The floor is the standard library, which is never rewritten.

The largest single deferrable allocators are
`protobuf/reflect/protodesc` at 135 KB and `protobuf/types/descriptorpb` at
74 KB -- between them nearly half of everything recoverable, and paid today by
every command in the busybox whether or not it has ever heard of protobuf.

## Consequences

- `GLOSSARY.md`'s **Rewrite** entry says "Only commands are rewritten; the
  packages they import are copied without rewriting". That stops being true
  and must be amended when this lands, along with new entries for the hook
  and the eager set.

- Rewriting reaches machine-written code for the first time. Commands are
  hand-written; library packages include generated protobuf sources and other
  output no one proofread. The type inference that hoists package-level
  variables is where issue #9's collisions came from, and it now runs over far
  stranger input.

- The blast radius of a rewrite defect grows from one command to every command
  importing the affected library. A differential check — build each fixture
  busybox with and without laziness and compare output, exit status and
  `GODEBUG=inittrace=1` — is written before any library is rewritten, and runs
  in the unit suite so that it guards every pull request rather than only the
  `-external` runs. The trace is the weaker half of that check: a deferred
  `init` has become an ordinary call and does not appear in it at all, so only
  the output can distinguish one that ran from one that was skipped. Some
  fixture's libraries therefore have to have initialization that reaches the
  command's output, and until this lands none of them did.

- A library now calls another library's hook, which the first draft of this
  decision forbade. The cost is that the generated code is a graph walk rather
  than a list, and that each rewritten package carries one generated boolean
  of state. The benefit is that it compiles.

- The capability lands staged and off by default, as `-lazy-init`, taking
  optional package patterns. **The staging is by which packages are deferred,
  not by which half of a package** — the patterns are the dial, and a package
  named by them gives up its variables and its `init` together. The default
  flips on evidence in a later release, not in the one that introduces it.

- The memory claim is about heap allocated during `init`, not about the size
  of the busybox. Adding the grpc command above grew peak RSS by 6.2 MB, of
  which only about 365 KB is `init` heap; the rest is text the linker keeps
  whether or not anything calls it. Deferring `init` recovers the former and
  none of the latter.

- Deferred packages initialize in a valid Go initialization order, but not in
  the one Go itself would have chosen. Every package still runs after
  everything it imports; two packages that do not import one another may swap,
  for the reason given under How. So a program whose packages have
  order-dependent side effects that they do not declare by importing one
  another can observe the difference. That is the smaller of the two changes
  this decision makes to when a package initializes -- the larger is that a
  deferred `init` runs after program start rather than before `main` -- and
  `-lazy-init` is off by default. It is a semantic change all the same, not an
  implementation detail. `lazyinit.CallOrder`'s documentation and
  `TestCallOrderDiffersFromGosPackageInitializationOrder` hold the detail and
  the measured example.

- This depends on [0003](0003-one-command-runs-per-process.md). If a busybox
  ever dispatches twice in one process, a deferred `init` runs twice where Go
  would have run it once, and this decision has to be revisited. The guarding
  boolean makes that a no-op rather than a repeat, but the second command
  would then see state the first had already mutated, which is 0003's problem
  and not this one's.
