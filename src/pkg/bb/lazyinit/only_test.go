// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lazyinit

import (
	"slices"
	"testing"

	"golang.org/x/tools/go/packages"
)

// deferredPaths returns the deferred set as a sorted slice. It is the
// complement of eagerPaths over the packages in the busybox, and the
// complement is what DeferOnly is specified in terms of.
func deferredPaths(p *Plan) []string {
	var out []string
	for path := range p.pkgs {
		if p.Deferred(path) {
			out = append(out, path)
		}
	}
	slices.Sort(out)
	return out
}

// namedGraph is the shape the libinit fixture has, which is the one step 4
// lands against:
//
//	first  -> lonely
//	       -> shared
//	second -> dependent -> shared
//	       -> shared
//
// lonely is reached by one command, shared by both, and dependent sits
// between second and shared so that there is an importer to drag along.
func namedGraph(t *testing.T) []*packages.Package {
	t.Helper()
	shared := plain(t, "example.com/m/pkg/shared")
	lonely := plain(t, "example.com/m/pkg/lonely")
	dependent := imports(plain(t, "example.com/m/pkg/dependent"), shared)
	first := imports(plain(t, "example.com/m/cmd/first"), lonely, shared)
	second := imports(plain(t, "example.com/m/cmd/second"), dependent, shared)
	return []*packages.Package{first, second}
}

// Naming one package defers that package and nothing else that did not have
// to come with it. This is the whole of what step 4 claims.
func TestDeferOnlyDefersTheNamedPackage(t *testing.T) {
	p := New(namedGraph(t), DeferOnly([]string{"example.com/m/pkg/lonely"}))

	// first imports lonely, so it cannot stay eager; second and everything
	// under it is untouched.
	want := []string{
		"example.com/m/cmd/first",
		"example.com/m/cmd/second",
		"example.com/m/pkg/lonely",
	}
	if got := deferredPaths(p); !slices.Equal(got, want) {
		t.Errorf("deferred set = %v, want %v", got, want)
	}

	for _, eager := range []string{"example.com/m/pkg/shared", "example.com/m/pkg/dependent"} {
		if !p.Eager(eager) {
			t.Errorf("Eager(%q) = false, want true: it was not named", eager)
		}
		if got, want := p.EagerReason(eager), eagerNotNamed; got != want {
			t.Errorf("EagerReason(%q) = %q, want %q", eager, got, want)
		}
	}
}

// Naming a package defers every package that can reach it, however far away.
// Without this an eager package would import a deferred one, read it before
// it had been initialized, and do so silently.
func TestDeferOnlyCarriesTheImportersUp(t *testing.T) {
	// A chain long enough that one step of closure is not enough:
	//
	//	cmd -> top -> mid -> bottom
	bottom := plain(t, "example.com/m/bottom")
	mid := imports(plain(t, "example.com/m/mid"), bottom)
	top := imports(plain(t, "example.com/m/top"), mid)
	cmd := imports(plain(t, "example.com/m/cmd"), top)

	p := New([]*packages.Package{cmd}, DeferOnly([]string{"example.com/m/bottom"}))

	for _, path := range []string{"example.com/m/bottom", "example.com/m/mid", "example.com/m/top"} {
		if !p.Deferred(path) {
			t.Errorf("Deferred(%q) = false, want true", path)
		}
	}
}

// The invariant survives narrowing. Marking packages eager one at a time is
// exactly how it would be lost, so it is asserted again here rather than
// assumed to hold because New establishes it.
func TestNarrowingKeepsTheEagerSetClosedDownward(t *testing.T) {
	for _, tt := range []struct {
		name  string
		cmds  []*packages.Package
		named []string
	}{
		{"a leaf", namedGraph(t), []string{"example.com/m/pkg/lonely"}},
		{"a package with importers", namedGraph(t), []string{"example.com/m/pkg/shared"}},
		{"nothing named", namedGraph(t), nil},
		{"a path that is not here", namedGraph(t), []string{"example.com/m/nope"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := New(tt.cmds, DeferOnly(tt.named))

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

// Naming the package both commands reach drags both of them and dependent
// along, which is the case where narrowing recovers almost nothing and the
// user should still get what they asked for.
func TestDeferOnlyANamedPackageWithTwoPaths(t *testing.T) {
	p := New(namedGraph(t), DeferOnly([]string{"example.com/m/pkg/shared"}))

	want := []string{
		"example.com/m/cmd/first",
		"example.com/m/cmd/second",
		"example.com/m/pkg/dependent",
		"example.com/m/pkg/shared",
	}
	if got := deferredPaths(p); !slices.Equal(got, want) {
		t.Errorf("deferred set = %v, want %v", got, want)
	}
	if !p.Eager("example.com/m/pkg/lonely") {
		t.Error(`Eager("lonely") = false, want true: nothing named it and nothing it imports was named`)
	}
}

// With no patterns nothing is deferred, so -lazy-init with an empty list
// builds what the baseline builds. The flag being off by default is the whole
// of the staging, and an empty list that deferred everything would be the
// opposite of off.
func TestDeferOnlyNothingDefersNoLibrary(t *testing.T) {
	p := New(namedGraph(t), DeferOnly(nil))

	for path := range p.pkgs {
		if p.isCommand(path) {
			continue
		}
		if p.Deferred(path) {
			t.Errorf("Deferred(%q) = true, want false: no pattern named it", path)
		}
	}
}

// A command is rewritten whether or not the patterns name it, so it is never
// reported eager. Hooks would still be right either way; the report at the
// end of a build would not, and it is what a user reads.
func TestNarrowingNeverCallsACommandEager(t *testing.T) {
	p := New(namedGraph(t), DeferOnly([]string{"example.com/m/pkg/lonely"}))

	// second reaches nothing that was named, so it is the command that
	// would be marked if commands were marked at all.
	if p.Eager("example.com/m/cmd/second") {
		t.Errorf("Eager(second) = true, want false: a command is not a library")
	}
}

// Deferred is not the negation of Eager, and the difference is what lets a
// build tell "you asked for a package that is not here" from "you asked for
// one that cannot be deferred". Both are refusals and they need different
// messages.
func TestDeferredSeparatesAbsentFromEager(t *testing.T) {
	refused := withOtherFiles(pkg(t, "example.com/m/refused", "package p\n"), "/x/a.s")
	cmd := imports(plain(t, "example.com/m/cmd"), refused)

	p := New([]*packages.Package{cmd}, DeferOnly([]string{"example.com/m/refused"}))

	if p.Deferred("example.com/m/refused") {
		t.Error("Deferred(refused) = true, want false: it carries an assembly file")
	}
	if got := p.EagerReason("example.com/m/refused"); got == "" {
		t.Error("EagerReason(refused) is empty, want the refusal: an empty reason means the package is not in this busybox")
	}

	if p.Deferred("example.com/m/absent") {
		t.Error("Deferred(absent) = true, want false")
	}
	if got := p.EagerReason("example.com/m/absent"); got != "" {
		t.Errorf("EagerReason(absent) = %q, want empty: it names no package here", got)
	}
}

// Naming a package that cannot be deferred does not quietly defer its
// importers instead. The importers come up only behind a package that
// actually moved.
func TestDeferOnlyARefusedPackageMovesNothing(t *testing.T) {
	refused := withOtherFiles(pkg(t, "example.com/m/refused", "package p\n"), "/x/a.s")
	middle := imports(plain(t, "example.com/m/middle"), refused)
	cmd := imports(plain(t, "example.com/m/cmd"), middle)

	p := New([]*packages.Package{cmd}, DeferOnly([]string{"example.com/m/refused"}))

	if p.Deferred("example.com/m/middle") {
		t.Error("Deferred(middle) = true, want false: the package it imports never moved")
	}
}

// Hooks reads the narrowed set, because it is what the generated code is
// written from. A hook calling a package that was left eager would run its
// initialization a second time.
func TestHooksFollowTheNarrowedSet(t *testing.T) {
	p := New(namedGraph(t), DeferOnly([]string{"example.com/m/pkg/lonely"}))

	got := p.Hooks("example.com/m/cmd/first")
	want := []string{"example.com/m/pkg/lonely"}
	if !slices.Equal(got, want) {
		t.Errorf("Hooks(first) = %v, want %v: shared stayed eager", got, want)
	}
	if got := p.Hooks("example.com/m/cmd/second"); len(got) != 0 {
		t.Errorf("Hooks(second) = %v, want none: it reaches nothing that was named", got)
	}
}
