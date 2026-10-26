// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lazyinit

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/u-root/gobusybox/src/pkg/golang"
	"golang.org/x/tools/go/packages"
)

// loadMode is the mode findpkg loads commands with. Keeping the same mode here
// is the point of this file: the decision is only as good as the graph it is
// given, and the graph depends on how it was resolved.
const loadMode = packages.NeedName | packages.NeedImports | packages.NeedFiles |
	packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax |
	packages.NeedTypesInfo | packages.NeedCompiledGoFiles | packages.NeedModule |
	packages.NeedEmbedFiles

// writeReadOnlyModule writes a self-contained module and takes away write
// permission, so that source looks as it does when it comes from the module
// cache rather than from a checkout.
//
// Self-contained on purpose: it requires nothing but the standard library, so
// the test needs no network and no populated module cache, and cannot fail for
// a reason that has nothing to do with what it is checking.
func writeReadOnlyModule(t *testing.T) string {
	t.Helper()

	return writeReadOnlyTree(t, map[string]string{
		"go.mod": "module example.com/testmod\n\ngo 1.21\n",

		// The command reaches all three shapes.
		"cmd/first/main.go": `package main

import (
	"fmt"

	"example.com/testmod/pkg/mpuller"
	"example.com/testmod/pkg/mvictim"
	"example.com/testmod/pkg/plain"
	"example.com/testmod/pkg/puller"
	"example.com/testmod/pkg/victim"
	"example.com/testmod/pkg/withasm"
)

func main() {
	fmt.Println(plain.V, puller.V, victim.V, withasm.V, mpuller.V, mvictim.V)
}
`,
		// Deferrable: nothing reaches it except through imports.
		"pkg/plain/plain.go": `package plain

import "example.com/testmod/pkg/under"

var V = "plain" + under.V
`,
		"pkg/under/under.go": `package under

var V = "under"
`,
		// Refused for its assembly file, which drags under it.
		"pkg/withasm/withasm.go": `package withasm

import "example.com/testmod/pkg/beneath"

var V = "withasm" + beneath.V
`,
		// No architecture in the name and no build constraint, on
		// purpose. go/packages filters OtherFiles by build constraints,
		// so an empty_amd64.s would leave withasm unrefused on every
		// other host and take the closure assertions below with it.
		// Measured on Go 1.27: with the _amd64 suffix go list reports
		// SFiles=[] for arm64, arm and riscv64.
		"pkg/withasm/empty.s": "// This file exists so that go list reports an assembly file.\n",
		"pkg/beneath/beneath.go": `package beneath

var V = "beneath"
`,
		// Refused for its directive, which also names a target the import
		// graph cannot reach.
		"pkg/puller/puller.go": `package puller

import _ "unsafe"

var V = "puller"

//go:linkname exported example.com/testmod/pkg/victim.pulled
func exported() {}
`,
		// Imported by the command, so it is in the busybox and would be
		// deferred on its own account. It is named by puller's directive,
		// which no import edge records, so only that rule makes it eager --
		// and its own import has to come with it.
		"pkg/victim/victim.go": `package victim

import "example.com/testmod/pkg/victimdep"

var V = "victim" + victimdep.V
`,
		"pkg/victimdep/victimdep.go": `package victimdep

var V = "victimdep"
`,
		// The same edge, but to a method. The linker symbol for one
		// carries the receiver, so the package path is neither the text
		// before the first dot nor the text before the last.
		"pkg/mpuller/mpuller.go": `package mpuller

import _ "unsafe"

var V = "mpuller"

//go:linkname ready example.com/testmod/pkg/mvictim.(*T).Ready
func ready(t *mvictimT) int

type mvictimT struct{ N int }
`,
		"pkg/mvictim/mvictim.go": `package mvictim

type T struct{ N int }

func (t *T) Ready() int { return t.N }

var V = "mvictim"
`,
	})
}

// writeReadOnlyTree writes files into a fresh directory and takes away write
// permission from the whole of it.
func writeReadOnlyTree(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	// t.TempDir's own cleanup cannot remove a tree it may not write to, and
	// Cleanup runs last-registered-first, so this has to be registered after
	// it to run before it. Best effort: if a chmod fails here, t.TempDir's
	// cleanup reports the removal failure, which is the better message.
	t.Cleanup(func() {
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err == nil {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	})

	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	// Directories read-only last, or the files could not have been written.
	// Checked rather than best effort: a tree that stayed writable would
	// still pass every assertion below, and would quietly stop standing in
	// for the module cache.
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(path, 0o555)
		}
		return nil
	}); err != nil {
		t.Fatalf("making the fixture read-only: %v", err)
	}
	return dir
}

// gomodbb resolves packages in a context makebb never produces: a working
// directory that is not the caller's, GOWORK off, and source it may not write
// to. The eager set is a closure over the import graph, and that graph is
// whatever go/packages resolves in exactly this context, so the decision has
// to be checked here and not only against a checkout.
//
// See https://github.com/u-root/gobusybox/issues/165 -- gomodbb.go:481 does
// env.Apply(golang.WithWorkingDir(modDir), golang.WithGOWORK("off")) on the
// very Environ that reaches the engine.
func TestDecidesInGomodbbResolutionContext(t *testing.T) {
	dir := writeReadOnlyModule(t)

	env := golang.Default(golang.WithGO111MODULE("on"))
	env.Apply(golang.WithWorkingDir(dir), golang.WithGOWORK("off"))

	pkgs, err := env.Lookup(loadMode, "./cmd/first")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("loaded %d packages, want 1", len(pkgs))
	}
	for _, e := range pkgs[0].Errors {
		t.Errorf("loading the fixture reported %v", e)
	}

	p := New(pkgs)

	t.Run("refusals are seen in read-only source", func(t *testing.T) {
		// The two refusals have to survive the resolution context, because
		// this is the only place the files come from a directory the build
		// may not write to.
		if got := p.EagerReason("example.com/testmod/pkg/withasm"); got != refusedAssembly {
			t.Errorf("withasm eager reason = %q, want %q", got, refusedAssembly)
		}
		if got := p.EagerReason("example.com/testmod/pkg/puller"); got != refusedLinkname {
			t.Errorf("puller eager reason = %q, want %q", got, refusedLinkname)
		}
	})

	t.Run("closure runs downward", func(t *testing.T) {
		if !p.Eager("example.com/testmod/pkg/beneath") {
			t.Error("beneath is lazy, but an eager package imports it")
		}
		if !p.Eager("example.com/testmod/pkg/victim") {
			t.Error("victim is lazy, but a two-argument //go:linkname names it")
		}
		if !p.Eager("example.com/testmod/pkg/victimdep") {
			t.Error("victimdep is lazy, but the eager victim imports it")
		}
		for _, lazy := range []string{
			"example.com/testmod/pkg/plain",
			"example.com/testmod/pkg/under",
		} {
			if p.Eager(lazy) {
				t.Errorf("%q is eager; nothing refuses it", lazy)
			}
		}
	})

	// The symbol here comes from a real parse of real source, so this is
	// what establishes that a method's directive is written the way the
	// resolution in Plan.linknameTarget expects.
	t.Run("a directive naming a method reaches its package", func(t *testing.T) {
		if got := p.EagerReason("example.com/testmod/pkg/mvictim"); got != eagerLinknameTarget {
			t.Errorf("mvictim eager reason = %q, want %q -- the symbol is "+
				"example.com/testmod/pkg/mvictim.(*T).Ready, and neither its first "+
				"dot nor its last separates the package from the name", got, eagerLinknameTarget)
		}
	})

	t.Run("the invariant holds on a really resolved graph", func(t *testing.T) {
		for path, pkg := range p.pkgs {
			if !p.Eager(path) {
				continue
			}
			for _, imp := range sortedImports(pkg) {
				if !p.Eager(imp) {
					t.Errorf("eager %q imports lazy %q", path, imp)
				}
			}
		}
	})

	// The hand-built fixtures elsewhere decide for themselves which
	// packages have a module, so on their own they only prove the rule is
	// applied consistently, not that it is the right rule. Here the go
	// tool fills Module in, so this is what establishes that standard
	// library packages are the ones it leaves nil.
	t.Run("the standard library is told apart by the go tool", func(t *testing.T) {
		var std, inModule int
		for path, pkg := range p.pkgs {
			if pkg.Module == nil {
				std++
				if !p.Eager(path) {
					t.Errorf("%q has no module, so it is standard library and must be eager", path)
				}
				if got := p.EagerReason(path); got != eagerStandardLibrary {
					t.Errorf("EagerReason(%q) = %q, want %q", path, got, eagerStandardLibrary)
				}
				continue
			}
			inModule++
			if p.EagerReason(path) == eagerStandardLibrary {
				t.Errorf("%q belongs to module %q but was called standard library", path, pkg.Module.Path)
			}
		}
		// fmt and its own imports come in through cmd/first.
		if std == 0 {
			t.Error("no standard library package in the graph; this guards nothing")
		}
		if inModule == 0 {
			t.Error("no module package in the graph; this guards nothing")
		}
		t.Logf("%d standard library packages, %d in a module", std, inModule)
	})

	t.Run("ordering agrees with the resolved import graph", func(t *testing.T) {
		order := p.CallOrder("example.com/testmod/cmd/first")
		if len(order) == 0 {
			t.Fatal("nothing to initialize; the fixture has deferrable packages")
		}
		seen := map[string]bool{}
		for _, path := range order {
			for _, imp := range sortedImports(p.pkgs[path]) {
				if slices.Contains(order, imp) && !seen[imp] {
					t.Errorf("%q runs before its import %q; order = %v", path, imp, order)
				}
			}
			seen[path] = true
		}
		// under is reached only through plain, so a walk that stopped at
		// direct imports would miss it.
		if !slices.Contains(order, "example.com/testmod/pkg/under") {
			t.Errorf("order = %v, missing the indirectly reached package", order)
		}
	})
}

// TestAVendoredPathAndItsModuleCopyAreDifferentPackages is the import-key case
// on a real load, where the go tool decides the identities rather than the
// fixture.
//
// The standard library is vendored into GOROOT, so net/http's source imports
// "golang.org/x/net/http2/hpack" and gets the package whose path is
// "vendor/golang.org/x/net/http2/hpack". A busybox may hold the module copy
// under the bare path at the same time -- any command importing
// golang.org/x/net/... does -- and then the key names a package in the busybox
// that net/http does not import.
//
// The fixture is the module copy, by declaring itself to be that module, so
// this needs no network and no populated module cache: the only other package
// involved is net/http, which is in GOROOT.
//
// What goes wrong without the distinction is quiet. net/http is eager, being
// standard library, so following its key hands eagerness to the module copy
// and then to everything the module copy imports. Nothing fails to build;
// there is only less laziness than #165 promises, spread over a subtree.
func TestAVendoredPathAndItsModuleCopyAreDifferentPackages(t *testing.T) {
	dir := writeReadOnlyTree(t, map[string]string{
		"go.mod": "module golang.org/x/net/http2\n\ngo 1.21\n",
		"hpack/hpack.go": `package hpack

import "golang.org/x/net/http2/internal/beneath"

var V = "the module copy" + beneath.V
`,
		"internal/beneath/beneath.go": `package beneath

var V = "beneath"
`,
		"cmd/first/main.go": `package main

import (
	"fmt"
	"net/http"

	"golang.org/x/net/http2/hpack"
)

func main() { fmt.Println(http.MethodGet, hpack.V) }
`,
	})

	env := golang.Default(golang.WithGO111MODULE("on"))
	env.Apply(golang.WithWorkingDir(dir), golang.WithGOWORK("off"))

	pkgs, err := env.Lookup(loadMode, "./cmd/first")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("loaded %d packages, want 1", len(pkgs))
	}
	for _, e := range pkgs[0].Errors {
		t.Errorf("loading the fixture reported %v", e)
	}

	// The fixture only guards anything if the go tool really did resolve
	// one of these imports to a path that is not how it was written.
	var divergent int
	for _, pkg := range pkgs[0].Imports {
		for key, imp := range pkg.Imports {
			if key != imp.PkgPath {
				divergent++
			}
		}
	}
	if divergent == 0 {
		t.Fatal("no import in this graph resolves to a path other than its key; " +
			"the standard library may have stopped vendoring, and this test now guards nothing")
	}

	p := New(pkgs)

	if !p.Eager("vendor/golang.org/x/net/http2/hpack") {
		t.Error("the vendored copy is lazy, but eager net/http imports it")
	}
	for _, lazy := range []string{
		"golang.org/x/net/http2/hpack",
		"golang.org/x/net/http2/internal/beneath",
	} {
		if p.Eager(lazy) {
			t.Errorf("Eager(%q) = true (reason %q), want false: net/http imports the "+
				"vendored copy of that path, not this package", lazy, p.EagerReason(lazy))
		}
	}

	// And the eager set names packages, not import keys.
	for path := range p.eager {
		if _, ok := p.pkgs[path]; !ok {
			t.Errorf("eager set names %q, which is not a package in the busybox", path)
		}
	}
}

// TestTheFixtureRefusesOnEveryArchitecture keeps the fixture honest about the
// one thing it cannot show on the host it runs on.
//
// go/packages filters OtherFiles by build constraints, and a file named
// empty_amd64.s carries one implicitly. A fixture spelled that way exercises
// the refusal on amd64 and silently stops exercising it everywhere else:
// withasm is not refused, so beneath is not dragged eager, and the closure
// assertions in TestDecidesInGomodbbResolutionContext fail for a reason that
// has nothing to do with the code under test. Measured on Go 1.27: go list
// reports SFiles=[] for that name on arm64, arm and riscv64.
//
// Loading for a foreign architecture costs nothing but a second go list --
// nothing is compiled -- and it is the only way an amd64 runner can see this.
func TestTheFixtureRefusesOnEveryArchitecture(t *testing.T) {
	// Whatever this host is not, so the check cannot degrade into the
	// host's own architecture on a different runner.
	foreign := "arm64"
	if runtime.GOARCH == foreign {
		foreign = "amd64"
	}

	dir := writeReadOnlyModule(t)

	env := golang.Default(golang.WithGO111MODULE("on"))
	env.Apply(golang.WithWorkingDir(dir), golang.WithGOWORK("off"), golang.WithGOARCH(foreign))

	pkgs, err := env.Lookup(loadMode, "./cmd/first")
	if err != nil {
		t.Fatalf("Lookup for GOARCH=%s: %v", foreign, err)
	}

	p := New(pkgs)

	if got := p.EagerReason("example.com/testmod/pkg/withasm"); got != refusedAssembly {
		t.Errorf("GOARCH=%s: withasm eager reason = %q, want %q -- its assembly file "+
			"is excluded by a build constraint on this architecture, so the fixture "+
			"stops standing for a refused package", foreign, got, refusedAssembly)
	}
	if !p.Eager("example.com/testmod/pkg/beneath") {
		t.Errorf("GOARCH=%s: beneath is lazy, but the refused withasm imports it", foreign)
	}
}
