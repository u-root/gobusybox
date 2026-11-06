// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lazyinit

import (
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// imports records that p imports deps, as go/packages delivers it.
func imports(p *packages.Package, deps ...*packages.Package) *packages.Package {
	for _, d := range deps {
		p.Imports[d.PkgPath] = d
	}
	return p
}

// importsAs records that p imports q, written in p's source as key.
//
// go/packages keys Imports by "import paths appearing in the package's Go
// source files", which is not always the path of the package that import
// resolves to. importsAs is how that case is written here; imports is the
// ordinary case where the two agree.
func importsAs(p *packages.Package, key string, q *packages.Package) *packages.Package {
	p.Imports[key] = q
	return p
}

// plain is a package with nothing in it that affects the decision.
func plain(t *testing.T, path string) *packages.Package {
	t.Helper()
	return pkg(t, path, "package p\n")
}

// eagerPaths returns the eager set as a sorted slice, for comparison.
func eagerPaths(p *Plan) []string {
	var out []string
	for path := range p.eager {
		out = append(out, path)
	}
	slices.Sort(out)
	return out
}

func TestEagerSetIsClosedDownwardUnderImports(t *testing.T) {
	// The shape that matters: a refused package sits above an ordinary one,
	// so the ordinary one must be dragged eager with it.
	//
	//   cmd  ->  top  ->  mid  ->  bottom
	//        ->  refused  ->  undertow
	//        ->  fmt (standard library)
	bottom := plain(t, "example.com/m/bottom")
	mid := imports(plain(t, "example.com/m/mid"), bottom)
	top := imports(plain(t, "example.com/m/top"), mid)
	undertow := plain(t, "example.com/m/undertow")
	refused := imports(withAsm(t, pkg(t, "example.com/m/refused", "package p\n"), asmReachesPackageState), undertow)
	fmtPkg := stdlib(t, "fmt")
	cmd := imports(plain(t, "example.com/m/cmd/a"), top, refused, fmtPkg)

	p := New([]*packages.Package{cmd})

	want := []string{
		"example.com/m/refused",  // carries assembly
		"example.com/m/undertow", // dragged down by refused
		"fmt",                    // standard library is never rewritten
	}
	if got := eagerPaths(p); !slices.Equal(got, want) {
		t.Errorf("eager set = %v, want %v", got, want)
	}

	for _, lazy := range []string{"example.com/m/top", "example.com/m/mid", "example.com/m/bottom"} {
		if p.Eager(lazy) {
			t.Errorf("Eager(%q) = true, want false", lazy)
		}
	}
}

// The invariant the whole closure exists to keep. An eager package runs its
// init at program start, before any command is dispatched and so before any
// deferred init. If an eager package imported a deferred one, Go would have
// run the import first and this arrangement would run it second, against state
// the eager init has already read.
func TestNoEagerPackageImportsALazyOne(t *testing.T) {
	for _, tt := range []struct {
		name string
		cmd  *packages.Package
	}{
		{"refusal above a chain", eagerOverChain(t)},
		{"refusal in a diamond", eagerDiamond(t)},
		{"linkname target reached only by name", linknameGraph(t)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := New([]*packages.Package{tt.cmd})

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
	}
}

// A refused package above a chain: everything below it must come eager too.
func eagerOverChain(t *testing.T) *packages.Package {
	t.Helper()
	c := plain(t, "example.com/m/c")
	b := imports(plain(t, "example.com/m/b"), c)
	a := imports(withAsm(t, pkg(t, "example.com/m/a", "package p\n"), asmReachesPackageState), b)
	return imports(plain(t, "example.com/m/cmd"), a)
}

// A package reached both from a refused package and from an ordinary one. It
// must be eager, because of the refused path, even though the other path would
// have allowed it to be lazy.
func eagerDiamond(t *testing.T) *packages.Package {
	t.Helper()
	shared := plain(t, "example.com/m/shared")
	left := imports(withAsm(t, pkg(t, "example.com/m/left", "package p\n"), asmReachesPackageState), shared)
	right := imports(plain(t, "example.com/m/right"), shared)
	return imports(plain(t, "example.com/m/cmd"), left, right)
}

// The case the import graph cannot see. The puller is refused for carrying the
// directive, but that is not the point: its *target* is reached by name rather
// than by an import edge, so nothing in the graph drags the target eager.
func linknameGraph(t *testing.T) *packages.Package {
	t.Helper()
	victimDep := plain(t, "example.com/m/victimdep")
	victim := imports(plain(t, "example.com/m/victim"), victimDep)
	puller := pkg(t, "example.com/m/puller", `package p

import _ "unsafe"

//go:linkname mine example.com/m/victim.theirs
func mine()
`)
	// Note: puller does NOT import victim. That is the whole case.
	return imports(plain(t, "example.com/m/cmd"), puller, victim)
}

func TestTwoArgumentLinknameTargetJoinsTheEagerRoots(t *testing.T) {
	p := New([]*packages.Package{linknameGraph(t)})

	// The target is eager even though no package imports it from a refused
	// one, and its own imports come with it.
	for _, want := range []string{"example.com/m/victim", "example.com/m/victimdep"} {
		if !p.Eager(want) {
			t.Errorf("Eager(%q) = false, want true (named by a two-argument //go:linkname)", want)
		}
	}
	if got := p.EagerReason("example.com/m/victim"); got == "" {
		t.Error("EagerReason for a linkname target is empty; the build has to be able to say why")
	}
}

// ADR 0004 took this decision against a measurement: refusing unsafe would
// make 22 more packages eager for 36 KB of the 379 KB recoverable.
func TestImportingUnsafeDoesNotMakeAPackageEager(t *testing.T) {
	unsafePkg := stdlib(t, "unsafe")
	puns := imports(pkg(t, "example.com/m/puns", `package p

import "unsafe"

func F(p *int) uintptr { return uintptr(unsafe.Pointer(p)) }
`), unsafePkg)
	cmd := imports(plain(t, "example.com/m/cmd"), puns)

	p := New([]*packages.Package{cmd})

	if p.Eager("example.com/m/puns") {
		t.Error("a package importing unsafe was made eager; ADR 0004 refuses only linkname and assembly")
	}
	if !p.Eager("unsafe") {
		t.Error("unsafe itself must be eager: it is a standard library package")
	}
}

// TestEagerReasonDoesNotDependOnMapOrder pins the reason a package is eager,
// which EagerReason reports at the end of a build.
//
// Roots are collected into a map, and most roots are reachable from another
// root -- nearly all of the standard library is. If the downward closure were
// allowed to name a package before its own reason was recorded, the answer
// would be whichever importer Go's randomised map iteration reached first, so
// one input would explain itself differently from run to run. Measured on the
// resolved fixture in gomodbb_test.go, 30 of 67 packages changed their reason
// across 12 runs before this was fixed.
//
// The loop is the test: a single run has a fair chance of ordering the roots
// correctly by luck.
func TestEagerReasonDoesNotDependOnMapOrder(t *testing.T) {
	for i := 0; i < 50; i++ {
		// A chain of standard library packages, each a root in its own
		// right and each reachable from the one above.
		d := stdlib(t, "internal/abi")
		c := imports(stdlib(t, "runtime"), d)
		b := imports(stdlib(t, "errors"), c)
		a := imports(stdlib(t, "fmt"), b)
		// And a refusal below an eager package, which must blame its
		// own assembly rather than the importer.
		refused := withAsm(t, pkg(t, "example.com/m/refused", "package p\n"), asmReachesPackageState)
		mid := imports(plain(t, "example.com/m/mid"), refused)
		eagerAbove := imports(withAsm(t, pkg(t, "example.com/m/above", "package p\n"), asmReachesPackageState), mid)
		cmd := imports(plain(t, "example.com/m/cmd"), a, eagerAbove)

		p := New([]*packages.Package{cmd})

		for _, path := range []string{"fmt", "errors", "runtime", "internal/abi"} {
			if got := p.EagerReason(path); got != eagerStandardLibrary {
				t.Fatalf("run %d: EagerReason(%q) = %q, want %q", i, path, got, eagerStandardLibrary)
			}
		}
		if got := p.EagerReason("example.com/m/refused"); !strings.Contains(got, "assembly file reading") {
			t.Fatalf("run %d: EagerReason(refused) = %q, want the reason its own assembly gives", i, got)
		}
		// The one that genuinely is eager only by import keeps saying so.
		if got := p.EagerReason("example.com/m/mid"); got != eagerImportedByEager {
			t.Fatalf("run %d: EagerReason(mid) = %q, want %q", i, got, eagerImportedByEager)
		}
	}
}

func TestStandardLibraryIsAlwaysEager(t *testing.T) {
	for _, path := range []string{"fmt", "net/http", "internal/poll", "unsafe"} {
		std := stdlib(t, path)
		cmd := imports(plain(t, "example.com/m/cmd"), std)
		p := New([]*packages.Package{cmd})
		if !p.Eager(path) {
			t.Errorf("Eager(%q) = false, want true", path)
		}
	}
	// And the test above is only meaningful if the rule discriminates.
	for _, path := range []string{"example.com/m/lib", "golang.org/x/sys/unix"} {
		lib := plain(t, path)
		cmd := imports(plain(t, "example.com/m/cmd"), lib)
		p := New([]*packages.Package{cmd})
		if p.Eager(path) {
			t.Errorf("Eager(%q) = true, want false: it belongs to a module, so it is not standard library", path)
		}
	}
}

// TestAModuleWithoutADotIsNotStandardLibrary is the case that rules out
// deciding this from the import path.
//
// A module path only has to look like a domain when the go tool has to fetch
// it, so `module nodot` is legal for anything built locally, and `go work`
// members and replace targets reach gobusybox exactly that way. Asking
// whether the first element of the path contains a dot takes every package in
// such a module for standard library.
//
// bb.collectDeps carried that guess and left these packages out of the vendor
// directory, so the generated module failed with "package nodot/pkg/hello is
// not in std". 7b24c96 replaced it there; this keeps it from coming back
// here, where the cost is different and quieter -- a package wrongly called
// standard library is made eager, silently losing the laziness this package
// exists to decide.
func TestAModuleWithoutADotIsNotStandardLibrary(t *testing.T) {
	lib := plain(t, "nodot/pkg/hello")
	cmd := imports(plain(t, "nodot/cmd/hi"), lib)

	p := New([]*packages.Package{cmd})

	if p.Eager("nodot/pkg/hello") {
		t.Errorf("Eager(%q) = true, want false: it belongs to module nodot, so it is not standard library (reason: %q)",
			"nodot/pkg/hello", p.EagerReason("nodot/pkg/hello"))
	}
}

// TestEagernessFollowsResolvedPathsNotImportKeys is the case where the two
// namespaces a package can be named in come apart.
//
// go/packages keys Imports by the path written in the importer's source, and
// that is not always the path of the package it resolves to. The standard
// library is vendored into GOROOT, so net/http's source says
// "golang.org/x/net/http2/hpack" and it gets the package whose path is
// "vendor/golang.org/x/net/http2/hpack". Measured on Go 1.27: loading
// net/http alone produces 28 such edges.
//
// That matters here because a busybox may also contain the *module* copy of
// one of those paths -- any command importing golang.org/x/net/... does it --
// and then the source key names a real package in the busybox that the
// importer does not import at all. Carrying eagerness along the key rather
// than along the resolved path hands it to the wrong package and then walks
// that package's imports, so the loss spreads across its whole subtree. It is
// silent: nothing fails to build, there is just less laziness than the
// measurements in #165 promise.
func TestEagernessFollowsResolvedPathsNotImportKeys(t *testing.T) {
	// GOROOT's copy, which is what net/http's import really resolves to.
	vendored := stdlib(t, "vendor/golang.org/x/net/http2/hpack")
	netHTTP := importsAs(stdlib(t, "net/http"), "golang.org/x/net/http2/hpack", vendored)

	// The module copy, under the bare path, with something beneath it so
	// that a walk down the wrong edge is visible past the first step.
	beneath := plain(t, "golang.org/x/net/internal/timeseries")
	moduleDep := imports(plain(t, "golang.org/x/net/http2/hpack"), beneath)

	cmd := imports(plain(t, "example.com/m/cmd/a"), netHTTP, moduleDep)

	p := New([]*packages.Package{cmd})

	if !p.Eager("vendor/golang.org/x/net/http2/hpack") {
		t.Error("the vendored copy is lazy, but eager net/http imports it")
	}
	for _, lazy := range []string{
		"golang.org/x/net/http2/hpack",
		"golang.org/x/net/internal/timeseries",
	} {
		if p.Eager(lazy) {
			t.Errorf("Eager(%q) = true (reason %q), want false: net/http imports the vendored copy, not this one",
				lazy, p.EagerReason(lazy))
		}
	}
}

// A path that appears only as an import key names no package in the busybox,
// so it must not appear in the eager set at all. Its presence there would be
// harmless on its own, but it is the same mistake as the test above and
// cheaper to see: the eager set is a set of packages, and that path is not
// one.
func TestTheEagerSetHoldsOnlyPackagesThatAreInTheBusybox(t *testing.T) {
	vendored := stdlib(t, "vendor/golang.org/x/net/idna")
	netHTTP := importsAs(stdlib(t, "net/http"), "golang.org/x/net/idna", vendored)
	cmd := imports(plain(t, "example.com/m/cmd/a"), netHTTP)

	p := New([]*packages.Package{cmd})

	for _, path := range eagerPaths(p) {
		if _, ok := p.pkgs[path]; !ok {
			t.Errorf("eager set names %q, which is not a package in the busybox", path)
		}
	}
}

// TestLinknameTargetNamingAMethodIsResolved covers the shape the previous
// parse could not read.
//
// A //go:linkname may name a method, and the linker symbol for one carries the
// receiver: example.com/m/victim.(*T).Ready. Taking everything before the
// final dot yields "example.com/m/victim.(*T)", which is no package, so the
// target was dropped and the victim stayed deferred while an eager puller
// could call into it during startup.
//
// Confirmed against the toolchain rather than assumed: a package carrying
// //go:linkname ready example.com/lnmethod/victim.(*T).Ready compiles, and
// dropping victim from the build fails at link time with "relocation target
// example.com/lnmethod/victim.(*T).Ready not defined" -- so the edge is real
// and the import graph does not have it.
func TestLinknameTargetNamingAMethodIsResolved(t *testing.T) {
	victimDep := plain(t, "example.com/m/victimdep")
	victim := imports(plain(t, "example.com/m/victim"), victimDep)
	puller := pkg(t, "example.com/m/puller", `package p

import _ "unsafe"

//go:linkname ready example.com/m/victim.(*T).Ready
func ready()
`)
	// puller does NOT import victim: that is what makes the directive the
	// only edge to it.
	cmd := imports(plain(t, "example.com/m/cmd/a"), puller, victim)

	p := New([]*packages.Package{cmd})

	if got := p.EagerReason("example.com/m/victim"); got != eagerLinknameTarget {
		t.Errorf("EagerReason(victim) = %q, want %q", got, eagerLinknameTarget)
	}
	if !p.Eager("example.com/m/victimdep") {
		t.Error("victimdep is lazy, but the eager victim imports it")
	}
}

// The longest package path wins. A package path may end in a dotted segment
// -- gopkg.in/yaml.v3 is the ordinary case -- so a shorter prefix of the same
// symbol can be a package too, and it is the wrong one.
func TestLinknameTargetResolvesToTheLongestPackagePath(t *testing.T) {
	inner := plain(t, "gopkg.in/yaml.v3")
	outer := plain(t, "gopkg.in/yaml")
	puller := pkg(t, "example.com/m/puller", `package p

import _ "unsafe"

//go:linkname mine gopkg.in/yaml.v3.Marshal
func mine()
`)
	cmd := imports(plain(t, "example.com/m/cmd/a"), puller, inner, outer)

	p := New([]*packages.Package{cmd})

	if got := p.EagerReason("gopkg.in/yaml.v3"); got != eagerLinknameTarget {
		t.Errorf("EagerReason(yaml.v3) = %q, want %q", got, eagerLinknameTarget)
	}
	if p.Eager("gopkg.in/yaml") {
		t.Error("gopkg.in/yaml was made eager; the directive names a symbol in gopkg.in/yaml.v3")
	}
}

// A target in no package of this busybox is not an error: it cannot be
// deferred by this build anyway. x/sys/unix's directives name the standard
// library, which is already eager.
//
// Two shapes reach that answer differently: a symbol whose dots yield no
// package here, and one with no dots to try at all.
func TestLinknameTargetOutsideTheBusyboxIsIgnored(t *testing.T) {
	for _, tt := range []struct {
		name   string
		symbol string
	}{
		{"a package that is not in this busybox", "example.com/elsewhere.theirs"},
		{"an object-file symbol that names no package", "abisymbol"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			puller := pkg(t, "example.com/m/puller", `package p

import _ "unsafe"

//go:linkname mine `+tt.symbol+`
func mine()
`)
			lib := plain(t, "example.com/m/lib")
			cmd := imports(plain(t, "example.com/m/cmd/a"), puller, lib)

			p := New([]*packages.Package{cmd})

			if p.Eager("example.com/elsewhere") {
				t.Error("a path naming no package in the busybox joined the eager set")
			}
			if p.Eager("example.com/m/lib") {
				t.Error("lib was dragged eager by a directive that does not name it")
			}
			// The puller itself is still refused for carrying one.
			if got := p.EagerReason("example.com/m/puller"); got != refusedLinkname {
				t.Errorf("EagerReason(puller) = %q, want %q", got, refusedLinkname)
			}
		})
	}
}

// An assembly file naming another package makes that package eager, the same
// way a two-argument //go:linkname does.
//
// Refusing only the package holding the assembly would move the hazard rather
// than close it: the puller would be eager and the package its assembly reads
// into would still be deferred, so the read would land on a package whose
// hook had not run. Nothing imports the victim here, so the downward closure
// cannot reach it either.
func TestAnAssemblyTargetJoinsTheEagerRoots(t *testing.T) {
	victimDep := plain(t, "example.com/m/victimdep")
	victim := imports(plain(t, "example.com/m/victim"), victimDep)
	puller := withAsm(t, pkg(t, "example.com/m/puller", "package p\n"),
		"TEXT \u00b7f(SB),$0\n\tCALL "+asmSym("example.com/m/victim.Ready")+"(SB)\n\tRET\n")
	cmd := imports(plain(t, "example.com/m/cmd"), puller, victim)

	p := New([]*packages.Package{cmd})

	for _, want := range []string{"example.com/m/victim", "example.com/m/victimdep"} {
		if !p.Eager(want) {
			t.Errorf("Eager(%q) = false, want true (named by another package's assembly)", want)
		}
	}
	if got := p.EagerReason("example.com/m/victim"); got != eagerAssemblyTarget {
		t.Errorf("EagerReason(victim) = %q, want %q", got, eagerAssemblyTarget)
	}
	if !p.Eager("example.com/m/puller") {
		t.Error("the package whose assembly names another is not eager itself")
	}
}

// A package whose assembly reaches nothing is deferred, which is the whole
// point of reading the file rather than refusing on its extension.
func TestSelfContainedAssemblyDoesNotRefuse(t *testing.T) {
	lib := withAsm(t, pkg(t, "example.com/m/lib", "package p\n"), asmSelfContained)
	cmd := imports(plain(t, "example.com/m/cmd"), lib)

	p := New([]*packages.Package{cmd})

	if p.Eager("example.com/m/lib") {
		t.Errorf("a package whose assembly only implements its own functions is eager: %q",
			p.EagerReason("example.com/m/lib"))
	}
}

// asmSym writes a Go symbol the way assembly has to spell it. The assembler
// rejects ASCII ".", "/" and "-" inside an identifier, so a fixture using
// them is testing a file the toolchain would never accept -- which is how a
// scanner that only understood the ASCII form went unnoticed.
func asmSym(name string) string {
	return strings.NewReplacer(".", "\u00b7", "/", "\u2215").Replace(name)
}

// The fixtures above must be assembly the toolchain would accept, or they
// prove nothing about what the scanner meets in a real package.
func TestFixturesUseTheEncodingTheAssemblerRequires(t *testing.T) {
	got := asmSym("example.com/m/victim.Ready")
	if want := "example\u00b7com\u2215m\u2215victim\u00b7Ready"; got != want {
		t.Errorf("asmSym = %q, want %q", got, want)
	}
	if strings.ContainsAny(got, "./") {
		t.Errorf("asmSym left an ASCII . or / in %q, which the assembler rejects", got)
	}
}

// An assembly symbol whose package is not in this busybox is ignored, the
// same way a linkname target outside it is. Nothing here can defer it, so
// naming it is not an error.
func TestAssemblyTargetOutsideTheBusyboxIsIgnored(t *testing.T) {
	puller := withAsm(t, pkg(t, "example.com/m/puller", "package p\n"),
		"TEXT \u00b7f(SB),$0\n\tCALL "+asmSym("example.com/elsewhere/absent.Ready")+"(SB)\n\tRET\n")
	cmd := imports(plain(t, "example.com/m/cmd"), puller)

	p := New([]*packages.Package{cmd})

	if p.Eager("example.com/elsewhere/absent") {
		t.Error("a package that is not in the busybox was made eager")
	}
	// The puller still refuses on its own account.
	if !p.Eager("example.com/m/puller") {
		t.Error("the package whose assembly names another is not eager itself")
	}
}

// A target named by two packages must get the same reason every run. The
// roots are built by walking a map, and Go randomizes that order, so the
// first reason recorded for a target would otherwise be a coin toss.
func TestAnAssemblyTargetsReasonDoesNotDependOnMapOrder(t *testing.T) {
	want := ""
	for i := range 50 {
		victim := plain(t, "example.com/m/victim")
		byAsm := withAsm(t, pkg(t, "example.com/m/apuller", "package p\n"),
			"TEXT \u00b7f(SB),$0\n\tCALL "+asmSym("example.com/m/victim.Ready")+"(SB)\n\tRET\n")
		byLinkname := pkg(t, "example.com/m/zpuller", "package p\n\n//go:linkname mine example.com/m/victim.theirs\nfunc mine()\n")
		cmd := imports(plain(t, "example.com/m/cmd"), byAsm, byLinkname, victim)

		got := New([]*packages.Package{cmd}).EagerReason("example.com/m/victim")
		if i == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("run %d: EagerReason(victim) = %q, run 0 said %q", i, got, want)
		}
	}
}

// A package refused for some other reason still has its assembly read, because
// the symbols name packages nothing else reaches.
//
// A .syso refuses the package outright. Stopping there would leave the
// package its assembly reads into deferred, which is the hazard this is
// supposed to close, and it would be invisible: the puller is eager either
// way, so only the victim's state tells the two apart.
func TestAssemblyIsReadEvenWhenThePackageIsAlreadyRefused(t *testing.T) {
	victim := plain(t, "example.com/m/victim")
	puller := withOtherFiles(
		withAsm(t, pkg(t, "example.com/m/puller", "package p\n"),
			"TEXT \u00b7f(SB),$0\n\tCALL "+asmSym("example.com/m/victim.Ready")+"(SB)\n\tRET\n"),
		"/x/blob.syso")
	cmd := imports(plain(t, "example.com/m/cmd"), puller, victim)

	p := New([]*packages.Package{cmd})

	if !p.Eager("example.com/m/victim") {
		t.Error("the package named by the assembly is deferred, because the scan stopped at the .syso")
	}
	// The .syso is the reason the puller itself is refused; the assembly
	// reason must not overwrite it.
	if got := p.EagerReason("example.com/m/puller"); !strings.Contains(got, "syso") {
		t.Errorf("EagerReason(puller) = %q, want the .syso reason", got)
	}
}
