// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lazyinit

import (
	"slices"
	"testing"

	"golang.org/x/tools/go/packages"
)

// diamondCmd builds a command whose deferred packages form a diamond over a
// chain, with one standard library package and one refused package mixed in.
//
//	cmd -> left  -> shared -> base
//	    -> right -> shared
//	    -> fmt              (standard library, so eager)
//	    -> refused -> under (assembly, so eager, and under comes with it)
func diamondCmd(t *testing.T) *packages.Package {
	t.Helper()
	base := plain(t, "example.com/m/base")
	shared := imports(plain(t, "example.com/m/shared"), base)
	left := imports(plain(t, "example.com/m/left"), shared)
	right := imports(plain(t, "example.com/m/right"), shared)
	fmtPkg := stdlib(t, "fmt")
	under := plain(t, "example.com/m/under")
	refused := imports(withOtherFiles(pkg(t, "example.com/m/refused", "package p\n"),
		"/x/r.s"), under)
	return imports(plain(t, "example.com/m/cmd/a"), left, right, fmtPkg, refused)
}

func TestCallOrderPutsEveryPackageAfterItsImports(t *testing.T) {
	cmd := diamondCmd(t)
	p := New([]*packages.Package{cmd})

	order := p.CallOrder(cmd.PkgPath)

	// The oracle is go/packages itself: for each package in the order, every
	// one of its imports that is also in the order must already have
	// appeared. That is the part of Go's initialization order the hooks do
	// reproduce -- see TestCallOrderDiffersFromGosPackageInitializationOrder
	// for the part they cannot -- and it is checked against the graph rather
	// than against a list written out by hand, which would only restate the
	// implementation.
	seen := map[string]bool{}
	for _, path := range order {
		for _, imp := range sortedImports(p.pkgs[path]) {
			if !slices.Contains(order, imp) {
				continue
			}
			if !seen[imp] {
				t.Errorf("%q runs before its import %q; order = %v", path, imp, order)
			}
		}
		seen[path] = true
	}
}

func TestCallOrderNamesEachPackageOnce(t *testing.T) {
	cmd := diamondCmd(t)
	p := New([]*packages.Package{cmd})

	order := p.CallOrder(cmd.PkgPath)

	count := map[string]int{}
	for _, path := range order {
		count[path]++
	}
	for path, n := range count {
		if n != 1 {
			t.Errorf("%q appears %d times in the call order, want 1", path, n)
		}
	}
	// shared is reached through both left and right. If the walk did not
	// remember what it had already emitted, this is where it would show.
	if count["example.com/m/shared"] != 1 {
		t.Errorf("the diamond's shared package appears %d times", count["example.com/m/shared"])
	}
}

func TestCallOrderHoldsOnlyDeferredPackages(t *testing.T) {
	cmd := diamondCmd(t)
	p := New([]*packages.Package{cmd})

	order := p.CallOrder(cmd.PkgPath)

	for _, path := range order {
		if p.Eager(path) {
			t.Errorf("%q is eager but appears in the call order; its init has already run", path)
		}
	}
	// Specifically the two kinds of eager package.
	for _, eager := range []string{"fmt", "example.com/m/refused", "example.com/m/under"} {
		if slices.Contains(order, eager) {
			t.Errorf("eager %q appears in the call order", eager)
		}
	}
	// And the deferred ones are all there, or the walk is not reaching them.
	for _, lazy := range []string{
		"example.com/m/base", "example.com/m/shared",
		"example.com/m/left", "example.com/m/right",
	} {
		if !slices.Contains(order, lazy) {
			t.Errorf("deferred %q is missing from the call order; nothing would initialize it", lazy)
		}
	}
}

// Two builds of one input have to agree, or the generated source churns and
// the busybox stops being reproducible.
func TestCallOrderIsDeterministic(t *testing.T) {
	want := New([]*packages.Package{diamondCmd(t)}).CallOrder("example.com/m/cmd/a")
	for i := range 8 {
		got := New([]*packages.Package{diamondCmd(t)}).CallOrder("example.com/m/cmd/a")
		if !slices.Equal(got, want) {
			t.Fatalf("run %d: order = %v, want %v", i, got, want)
		}
	}
}

// A command initializes what it imports and nothing else. This is the point of
// the whole exercise: 125 of the u-root busybox's 228 library packages are
// reached by exactly one command, and every command pays for all of them.
func TestCallOrderExcludesAnotherCommandsPackages(t *testing.T) {
	lonely := plain(t, "example.com/m/lonely")
	shared := plain(t, "example.com/m/shared")
	first := imports(plain(t, "example.com/m/cmd/first"), lonely, shared)
	second := imports(plain(t, "example.com/m/cmd/second"), shared)

	p := New([]*packages.Package{first, second})

	firstOrder := p.CallOrder("example.com/m/cmd/first")
	secondOrder := p.CallOrder("example.com/m/cmd/second")

	if !slices.Contains(firstOrder, "example.com/m/lonely") {
		t.Errorf("first does not initialize lonely, which it imports; order = %v", firstOrder)
	}
	if slices.Contains(secondOrder, "example.com/m/lonely") {
		t.Errorf("second initializes lonely, which it does not import; order = %v", secondOrder)
	}
	for _, order := range [][]string{firstOrder, secondOrder} {
		if !slices.Contains(order, "example.com/m/shared") {
			t.Errorf("shared is missing from %v", order)
		}
	}
}

func TestCallOrderOfAnUnknownCommandIsEmpty(t *testing.T) {
	p := New([]*packages.Package{diamondCmd(t)})
	if got := p.CallOrder("example.com/m/cmd/nonexistent"); got != nil {
		t.Errorf("CallOrder of an unknown command = %v, want nil", got)
	}
}

// Hooks is what the generated source consumes: the hooks one package's own
// hook has to call, which is its direct deferred imports and nothing further.
// The recursion reaches the rest, and has to, because a package may not import
// its indirect imports -- another module's internal/ packages are the case
// that breaks, and ADR 0004 records the compile error.
func TestHooksAreDirectDeferredImportsOnly(t *testing.T) {
	cmd := diamondCmd(t)
	p := New([]*packages.Package{cmd})

	// The command's own direct imports, minus the eager ones.
	want := []string{"example.com/m/left", "example.com/m/right"}
	if got := p.Hooks(cmd.PkgPath); !slices.Equal(got, want) {
		t.Errorf("Hooks(cmd) = %v, want %v", got, want)
	}

	// left reaches base, but only through shared, so it calls shared alone.
	want = []string{"example.com/m/shared"}
	if got := p.Hooks("example.com/m/left"); !slices.Equal(got, want) {
		t.Errorf("Hooks(left) = %v, want %v", got, want)
	}

	// base imports nothing deferred, so its hook calls nothing.
	if got := p.Hooks("example.com/m/base"); len(got) != 0 {
		t.Errorf("Hooks(base) = %v, want empty", got)
	}
}

func TestHooksAreInImportPathOrder(t *testing.T) {
	// Built in an order that is not the import path order, so that a walk
	// taking the map's iteration order would be caught.
	zulu := plain(t, "example.com/m/zulu")
	alpha := plain(t, "example.com/m/alpha")
	mike := plain(t, "example.com/m/mike")
	cmd := imports(plain(t, "example.com/m/cmd/a"), zulu, mike, alpha)

	p := New([]*packages.Package{cmd})

	want := []string{"example.com/m/alpha", "example.com/m/mike", "example.com/m/zulu"}
	if got := p.Hooks(cmd.PkgPath); !slices.Equal(got, want) {
		t.Errorf("Hooks = %v, want %v", got, want)
	}
}

// interleavedCmd is the graph that tells the two orders apart.
//
//	cmd -> a -> c
//	         -> e
//	    -> b -> d
//
// Go initializes c d b e a. The hook recursion cannot: a's hook runs c and e
// and then a, so a's three packages are contiguous, and Go puts d and b
// between c and e.
func interleavedCmd(t *testing.T) *packages.Package {
	t.Helper()
	c := plain(t, "example.com/m/c")
	d := plain(t, "example.com/m/d")
	e := plain(t, "example.com/m/e")
	a := imports(plain(t, "example.com/m/a"), c, e)
	b := imports(plain(t, "example.com/m/b"), d)
	return imports(plain(t, "example.com/m/cmd/a"), a, b)
}

// TestCallOrderDiffersFromGosPackageInitializationOrder records a limit of the
// design, so that nobody has to rediscover it from a bug.
//
// The specification does not stop at "imports first". It fixes a total order:
// "Given the list of all packages, sorted by import path, in each step the
// first uninitialized package in the list for which all imported packages (if
// any) are already initialized is initialized."
//
// The hooks cannot reproduce that, and not for want of trying. A hook may call
// only the hooks of its own direct imports -- ADR 0004, because another
// module's internal/ packages are not importable from outside it even inside
// vendor/ -- so every package's deferred imports are initialized contiguously
// with it. Go's rule interleaves them. For the graph above, Go runs
//
//	c d b e a
//
// measured with a real program, and the only orders a depth-first walk from
// the command can produce are c e a d b and d b c e a, whichever order the
// command's own hook calls a and b in.
//
// Both orders put every package after its imports, so both are valid Go
// initialization orders for programs that do not depend on more than that.
// This feature already moves a deferred package's initialization to after
// program start, which is a larger change to when things run than this is,
// and it is off by default. The cost is real all the same: a program whose
// packages have order-dependent side effects they do not declare by importing
// one another can observe the difference.
func TestCallOrderDiffersFromGosPackageInitializationOrder(t *testing.T) {
	p := New([]*packages.Package{interleavedCmd(t)})

	order := p.CallOrder("example.com/m/cmd/a")

	// What Go itself does, measured rather than derived from the rule.
	goOrder := []string{
		"example.com/m/c", "example.com/m/d", "example.com/m/b",
		"example.com/m/e", "example.com/m/a",
	}
	if slices.Equal(order, goOrder) {
		t.Fatalf("CallOrder = %v, which is Go's own order. If the hooks have been "+
			"made to reproduce it, this test and the comments in order.go are stale.", order)
	}

	// It is still a valid order, which is the claim that has to survive.
	if !slices.Equal(order, []string{
		"example.com/m/c", "example.com/m/e", "example.com/m/a",
		"example.com/m/d", "example.com/m/b",
	}) {
		t.Errorf("CallOrder = %v, want the depth-first walk the hooks perform", order)
	}
	if len(order) != len(goOrder) {
		t.Errorf("CallOrder has %d packages, Go initializes %d; the two orders must at "+
			"least agree on the set", len(order), len(goOrder))
	}
}
