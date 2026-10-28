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

	// Libraries only. first imports lonely so it cannot be made eager, but
	// a command is not deferred either -- it is rewritten whatever the
	// patterns say.
	want := []string{"example.com/m/pkg/lonely"}
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

// A command is rewritten whatever the patterns say, so naming one defers
// nothing. Saying so is the point: a -lazy-init that quietly moved nothing
// would satisfy every differential check, because a candidate identical to
// its baseline agrees with it on everything.
func TestNamingACommandIsRefused(t *testing.T) {
	p := New(namedGraph(t), DeferOnly([]string{"example.com/m/cmd/first"}))

	const cmd = "example.com/m/cmd/first"
	if p.Deferred(cmd) {
		t.Error("Deferred(first) = true, want false: a command is not a library")
	}
	if got := p.CannotDefer(cmd); !strings.Contains(got, "is a command") {
		t.Errorf("CannotDefer(first) = %q, want it to say this is a command", got)
	}
}

// The three refusals read differently because they send the reader somewhere
// different: a typo, a misunderstanding of the flag, or a rule doing its job.
func TestCannotDeferSaysWhich(t *testing.T) {
	refused := withOtherFiles(pkg(t, "example.com/m/refused", "package p\n"), "/x/a.s")
	lib := plain(t, "example.com/m/lib")
	cmd := imports(plain(t, "example.com/m/cmd"), refused, lib)

	p := New([]*packages.Package{cmd}, DeferOnly([]string{"example.com/m/lib"}))

	for _, tt := range []struct {
		path string
		want string
	}{
		{"example.com/m/absent", "is not imported by any command"},
		{"example.com/m/cmd", "is a command"},
		{"example.com/m/refused", "assembly"},
		{"example.com/m/lib", ""},
	} {
		got := p.CannotDefer(tt.path)
		if tt.want == "" {
			if got != "" {
				t.Errorf("CannotDefer(%q) = %q, want it to be deferred", tt.path, got)
			}
			continue
		}
		if !strings.Contains(got, tt.want) {
			t.Errorf("CannotDefer(%q) = %q, want it to contain %q", tt.path, got, tt.want)
		}
	}
}

// A command carrying assembly must not drag the whole busybox eager.
//
// A refusal says "this package cannot be rewritten to initialize later", which
// makes it an eager root and takes everything it imports with it. That cannot
// be said of a command: a command is rewritten whatever this decides, and its
// initialization already waits for its own dispatch, so it never initializes
// at program start and the premise does not hold. Letting it hold cost the
// whole busybox, since every library is reached by some command.
func TestARefusedCommandDoesNotDragItsLibrariesEager(t *testing.T) {
	deep := plain(t, "example.com/m/pkg/deep")
	mid := imports(plain(t, "example.com/m/pkg/mid"), deep)
	cmd := imports(withOtherFiles(pkg(t, "example.com/m/cmd/c", "package main\n"), "/x/a.s"), mid)

	p := New([]*packages.Package{cmd})

	for _, path := range []string{"example.com/m/pkg/mid", "example.com/m/pkg/deep"} {
		if p.Eager(path) {
			t.Errorf("Eager(%q) = true (%s), want false: the only refusal is in a command, which never initializes at program start",
				path, p.EagerReason(path))
		}
	}
}

// Its linkname targets still join the roots, though. Those name a package the
// command may never import, so nothing else would initialize it.
func TestACommandsLinknameTargetIsStillEager(t *testing.T) {
	victim := plain(t, "example.com/m/victim")
	cmd := imports(pkg(t, "example.com/m/cmd/c", `package main

import _ "unsafe"

//go:linkname mine example.com/m/victim.Ready
func mine()
`), victim)

	p := New([]*packages.Package{cmd})

	if !p.Eager("example.com/m/victim") {
		t.Error("Eager(victim) = false, want true: a command names it by a path the import graph does not have")
	}
	if p.Eager("example.com/m/cmd/c") {
		t.Error("Eager(cmd) = true, want false: a command is rewritten whatever its own source says")
	}
}

// A .syso is a precompiled object the linker takes whole: the same hazard as
// assembly with none of the source to read.
func TestASysoFileRefusesLaziness(t *testing.T) {
	syso := withOtherFiles(pkg(t, "example.com/m/syso", "package p\n"), "/x/blob.syso")
	cmd := imports(plain(t, "example.com/m/cmd"), syso)

	p := New([]*packages.Package{cmd})

	if !p.Eager("example.com/m/syso") {
		t.Error("Eager(syso) = false, want true")
	}
	if got := p.EagerReason("example.com/m/syso"); !strings.Contains(got, "syso") {
		t.Errorf("EagerReason(syso) = %q, want it to name the object file", got)
	}
}

// linknameTarget resolves the longest prefix that is a package here, and when
// the real target is not in this busybox that can be a shorter package than
// the one named. The effect is to make a package eager that need not be,
// which is the direction the refusal is deliberately over-broad in -- see
// ADR 0004 -- and there is nothing to resolve it with: the package that would
// settle it is the one that is absent.
//
// Pinned rather than fixed, so that the next reader finds the answer here
// instead of the question.
func TestLinknameTargetPrefersAShorterPackageWhenTheRealOneIsAbsent(t *testing.T) {
	yaml := plain(t, "gopkg.in/yaml")
	puller := pkg(t, "example.com/m/puller", `package p

import _ "unsafe"

//go:linkname mine gopkg.in/yaml.v3.Marshal
func mine()
`)
	cmd := imports(plain(t, "example.com/m/cmd"), yaml, puller)

	p := New([]*packages.Package{cmd})

	if !p.Eager("gopkg.in/yaml") {
		t.Error("Eager(gopkg.in/yaml) = false; the over-match is expected and safe, so this test is out of date rather than passing")
	}
	if got := p.EagerReason("gopkg.in/yaml"); !strings.Contains(got, "linkname") {
		t.Errorf("EagerReason = %q, want the linkname reason", got)
	}
}
