// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lazyinit

import (
	"slices"
	"testing"

	"golang.org/x/tools/go/packages"
)

// The matching rule is the go command's, so these cases are the ones
// `go help packages` describes plus the ones a path can get wrong.
func TestAPatternMatchesWhatTheGoCommandWouldMatch(t *testing.T) {
	for _, tt := range []struct {
		desc    string
		pattern string
		path    string
		want    bool
	}{
		{"an exact path matches itself", "example.com/m/pkg/a", "example.com/m/pkg/a", true},
		{"an exact path matches nothing else", "example.com/m/pkg/a", "example.com/m/pkg/b", false},
		{"an exact path does not reach its own subtree", "example.com/m/pkg/a", "example.com/m/pkg/a/b", false},

		// The special case: a trailing /... covers the root too.
		{"a trailing wildcard matches the root", "example.com/m/pkg/...", "example.com/m/pkg", true},
		{"a trailing wildcard matches a child", "example.com/m/pkg/...", "example.com/m/pkg/a", true},
		{"a trailing wildcard crosses slashes", "example.com/m/pkg/...", "example.com/m/pkg/a/b/c", true},
		{"a trailing wildcard stops at its prefix", "example.com/m/pkg/...", "example.com/m/other", false},

		// The prefix is a whole path element, not a string prefix. This
		// is the case a strings.HasPrefix implementation gets wrong.
		{"a sibling sharing a prefix is not in the subtree", "example.com/m/pkg/...", "example.com/m/pkgtwo", false},
		{"a sibling sharing a prefix is not in the subtree, with a child", "example.com/m/pkg/...", "example.com/m/pkgtwo/a", false},

		// A dot in a path is a dot, not a regexp wildcard. QuoteMeta is
		// what makes this true and it is easy to leave out.
		{"a dot in the pattern is literal", "example.com/m", "exampleXcom/m", false},
		{"a dot in the pattern matches a dot", "example.com/m", "example.com/m", true},

		{"a bare wildcard matches everything", "...", "example.com/m/pkg/a", true},
		{"a bare wildcard matches a single element", "...", "fmt", true},

		{"a wildcard in the middle crosses slashes", "example.com/.../a", "example.com/m/pkg/a", true},
		{"a wildcard in the middle still anchors the tail", "example.com/.../a", "example.com/m/pkg/b", false},

		{"a wildcard is not a glob", "example.com/m/pkg/*", "example.com/m/pkg/a", false},
		{"matching is case sensitive", "example.com/m/pkg/...", "example.com/M/pkg/a", false},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			if got := matches(tt.pattern)(tt.path); got != tt.want {
				t.Errorf("matches(%q)(%q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

func TestIsPatternSeparatesASubtreeFromAPath(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want bool
	}{
		{"example.com/m/pkg/a", false},
		{"example.com/m/pkg/...", true},
		{"...", true},
		{"example.com/.../a", true},
		// A glob is not this pattern language, so it is one path that
		// will match nothing and say so, rather than a subtree.
		{"example.com/m/pkg/*", false},
	} {
		if got := IsPattern(tt.in); got != tt.want {
			t.Errorf("IsPattern(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// Matching answers over the busybox's own packages, so a path that is not in
// it cannot be selected however well the text fits.
func TestMatchingSelectsOnlyPackagesInTheBusybox(t *testing.T) {
	plan := New(namedGraph(t))

	got := plan.Matching("example.com/m/pkg/...")
	want := []string{
		"example.com/m/pkg/dependent",
		"example.com/m/pkg/lonely",
		"example.com/m/pkg/shared",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Matching = %v, want %v", got, want)
	}

	if got := plan.Matching("example.com/elsewhere/..."); len(got) != 0 {
		t.Errorf("Matching of a subtree that is not here = %v, want none", got)
	}
}

// A command is rewritten whatever a pattern says, so a subtree holding both
// selects the libraries and leaves the commands alone.
func TestMatchingLeavesCommandsOut(t *testing.T) {
	plan := New(namedGraph(t))

	if got := plan.Matching("example.com/m/cmd/..."); len(got) != 0 {
		t.Errorf("Matching of a subtree of commands = %v, want none", got)
	}

	got := plan.Matching("...")
	want := []string{
		"example.com/m/pkg/dependent",
		"example.com/m/pkg/lonely",
		"example.com/m/pkg/shared",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Matching everything = %v, want the libraries %v", got, want)
	}
}

// Two builds of one input have to write the same tree, so the order here is
// the import path order and not the map's.
func TestMatchingIsInImportPathOrder(t *testing.T) {
	var pkgs []*packages.Package
	for _, p := range []string{"example.com/m/pkg/c", "example.com/m/pkg/a", "example.com/m/pkg/b"} {
		pkgs = append(pkgs, plain(t, p))
	}
	cmd := imports(plain(t, "example.com/m/cmd/one"), pkgs...)
	plan := New([]*packages.Package{cmd})

	for i := 0; i < 20; i++ {
		got := plan.Matching("example.com/m/pkg/...")
		want := []string{"example.com/m/pkg/a", "example.com/m/pkg/b", "example.com/m/pkg/c"}
		if !slices.Equal(got, want) {
			t.Fatalf("run %d: Matching = %v, want %v", i, got, want)
		}
	}
}

// A standard library package has no module, so it is in the busybox and
// always eager. A pattern selects it, and DeferOnly then cannot move it;
// keeping the two apart is what lets the caller report it.
func TestMatchingSelectsAnEagerPackage(t *testing.T) {
	fmtPkg := stdlib(t, "fmt")
	lib := imports(plain(t, "example.com/m/pkg/a"), fmtPkg)
	cmd := imports(plain(t, "example.com/m/cmd/one"), lib)
	plan := New([]*packages.Package{cmd})

	if got := plan.Matching("fmt"); !slices.Equal(got, []string{"fmt"}) {
		t.Errorf("Matching(fmt) = %v, want [fmt]", got)
	}
	if plan.Deferred("fmt") {
		t.Error("fmt is deferred, want eager")
	}
}
