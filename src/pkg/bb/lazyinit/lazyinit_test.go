// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lazyinit

import (
	"go/parser"
	"go/token"
	"slices"
	"testing"

	"golang.org/x/tools/go/packages"
)

// pkg builds a package whose source is src, as go/packages would deliver it:
// parsed syntax with comments, which is where the directives are.
//
// It belongs to a module, because every package outside the standard library
// does and that is how isStandardLibrary tells them apart. Only the presence
// of the module decides anything here, so the path is the package's own.
// stdlib builds the other kind.
//
// Hand-built rather than loaded from disk because the cases that matter here
// are a handful of lines each, and writing them as source makes what is being
// detected visible in the test rather than buried in a fixture directory.
func pkg(t *testing.T, path string, src ...string) *packages.Package {
	t.Helper()

	p := &packages.Package{
		PkgPath: path,
		Name:    path,
		Imports: map[string]*packages.Package{},
		Module:  &packages.Module{Path: path},
	}
	fset := token.NewFileSet()
	for i, s := range src {
		f, err := parser.ParseFile(fset, path, s, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s file %d: %v", path, i, err)
		}
		p.Syntax = append(p.Syntax, f)
		p.GoFiles = append(p.GoFiles, path)
	}
	return p
}

// stdlib is a standard library package. go/packages leaves Module nil for
// these and only for these, which is what makes them recognisable without
// guessing from the import path.
func stdlib(t *testing.T, path string) *packages.Package {
	t.Helper()
	p := pkg(t, path, "package p\n")
	p.Module = nil
	return p
}

// withOtherFiles is the assembly half: go/packages reports non-Go files that
// survive the build constraints in OtherFiles.
func withOtherFiles(p *packages.Package, files ...string) *packages.Package {
	p.OtherFiles = append(p.OtherFiles, files...)
	return p
}

func TestScanRefusesLinknameAndAssembly(t *testing.T) {
	for _, tt := range []struct {
		name string
		pkg  *packages.Package
		// want is the reason laziness is refused, or "" to allow it.
		want string
		// symbols are the remote symbols a two-argument directive names,
		// as written. Which package holds one is decided against the
		// loaded packages, not here; see Plan.linknameTarget.
		symbols []string
	}{
		{
			name: "plain package is deferrable",
			pkg: pkg(t, "example.com/plain", `package plain

func F() {}
`),
			want: "",
		},
		{
			name: "assembly file refuses",
			pkg: withOtherFiles(pkg(t, "example.com/asm", `package asm
`), "/tmp/example.com/asm/asm_linux_amd64.s"),
			want: refusedAssembly,
		},
		{
			name: "non-assembly other files do not refuse",
			pkg: withOtherFiles(pkg(t, "example.com/other", `package other
`), "/tmp/example.com/other/README.md", "/tmp/example.com/other/data.json"),
			want: "",
		},
		{
			name: "one-argument linkname refuses and names no target",
			pkg: pkg(t, "example.com/pull", `package pull

import _ "unsafe"

//go:linkname localname
func localname()
`),
			want: refusedLinkname,
		},
		{
			name: "two-argument linkname refuses and names its target",
			pkg: pkg(t, "example.com/push", `package push

import _ "unsafe"

//go:linkname mine example.com/victim.theirs
func mine()
`),
			want:    refusedLinkname,
			symbols: []string{"example.com/victim.theirs"},
		},
		{
			// The linker symbol for a method carries its receiver, so
			// neither the first dot nor the last separates the package
			// from the name. Confirmed to compile and link.
			name: "two-argument linkname naming a method",
			pkg: pkg(t, "example.com/method", `package method

import _ "unsafe"

//go:linkname ready example.com/victim.(*T).Ready
func ready()
`),
			want:    refusedLinkname,
			symbols: []string{"example.com/victim.(*T).Ready"},
		},
		{
			// The second argument is an object-file symbol name, and
			// not every one of those is pkg.name. Nothing in the
			// busybox can hold it, but scan does not decide that.
			name: "two-argument linkname naming a symbol with no package",
			pkg: pkg(t, "example.com/dotless", `package dotless

import _ "unsafe"

//go:linkname mine abisymbol
func mine()
`),
			want:    refusedLinkname,
			symbols: []string{"abisymbol"},
		},
		{
			name: "two-argument linkname into the standard library",
			pkg: pkg(t, "example.com/sys", `package sys

import _ "unsafe"

//go:linkname prlimit syscall.prlimit
func prlimit()
`),
			want:    refusedLinkname,
			symbols: []string{"syscall.prlimit"},
		},
		{
			// ADR 0004: unsafe lets a package do what it likes to memory
			// it already has. It does not let another package reach this
			// one outside the import graph, which is the property the
			// closure relies on.
			name: "importing unsafe alone does not refuse",
			pkg: pkg(t, "example.com/puns", `package puns

import "unsafe"

func F(p *int) uintptr { return uintptr(unsafe.Pointer(p)) }
`),
			want: "",
		},
		{
			name: "a directive in the second file is still found",
			pkg: pkg(t, "example.com/second", `package second
`, `package second

import _ "unsafe"

//go:linkname mine example.com/victim.theirs
func mine()
`),
			want:    refusedLinkname,
			symbols: []string{"example.com/victim.theirs"},
		},
		{
			// "//go:linkname" is a directive only in a comment of its own.
			// Prose mentioning it must not refuse a package, or writing
			// about this feature would make a package eager.
			name: "the word in prose is not a directive",
			pkg: pkg(t, "example.com/prose", `package prose

// This package deliberately avoids //go:linkname because it would
// make the package eager. See ADR 0004.
func F() {}
`),
			want: "",
		},
		{
			// A prefix match is not a match. Nothing spells a directive
			// this way today, but matching on the prefix alone would
			// refuse any future //go:linkname-something outright, and
			// silently, which is the failure this whole package is about.
			name: "a longer directive sharing the prefix is not this one",
			pkg: pkg(t, "example.com/prefix", `package prefix

//go:linknameisnotthisdirective mine example.com/victim.theirs
func mine() {}
`),
			want: "",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := scan(tt.pkg)
			if got.refusal != tt.want {
				t.Errorf("refusal = %q, want %q", got.refusal, tt.want)
			}
			if !slices.Equal(got.linknameSymbols, tt.symbols) {
				t.Errorf("linknameSymbols = %v, want %v", got.linknameSymbols, tt.symbols)
			}
		})
	}
}

// A package with no syntax at all must not panic the scan. unsafe is the real
// instance: it has no Go files, because the compiler supplies it.
func TestScanPackageWithoutSyntax(t *testing.T) {
	p := &packages.Package{PkgPath: "unsafe", Name: "unsafe"}
	if got := scan(p); got.refusal != "" || got.linknameSymbols != nil {
		t.Errorf("scan(unsafe) = %+v, want zero", got)
	}
}
