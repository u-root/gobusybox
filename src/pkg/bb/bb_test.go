// Copyright 2015-2024 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bb

import (
	"errors"
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/u-root/gobusybox/src/pkg/bb/bbinternal"
)

func cmd(pkgPath string, mod *packages.Module) *bbinternal.Package {
	return &bbinternal.Package{Pkg: &packages.Package{PkgPath: pkgPath, Module: mod}}
}

func TestCheckModules(t *testing.T) {
	someModule := &packages.Module{Path: "example.com/m", Version: "v1.0.0", GoVersion: "1.21"}

	for _, tt := range []struct {
		name string
		in   []*bbinternal.Package
		// Import paths the error must name, so that a user can tell
		// which commands are the problem.
		wantNamed []string
	}{
		{
			name: "all commands in a module",
			in: []*bbinternal.Package{
				cmd("example.com/m/cmd/a", someModule),
				cmd("example.com/m/cmd/b", someModule),
			},
		},
		{
			// Commands may span several modules; a workspace build
			// resolves one version per import path across all of
			// them.
			name: "commands spanning several modules",
			in: []*bbinternal.Package{
				cmd("example.com/m/cmd/a", someModule),
				cmd("other.com/n/cmd/b", &packages.Module{Path: "other.com/n", Version: "v2.0.0", GoVersion: "1.21"}),
			},
		},
		{
			name:      "no command in a module",
			in:        []*bbinternal.Package{cmd("example.com/m/cmd/a", nil)},
			wantNamed: []string{"example.com/m/cmd/a"},
		},
		{
			// The old check only rejected the mixed case, so a
			// build with no modules at all went through and was
			// resolved by GOPATH rules the vendor directory cannot
			// reproduce.
			name: "mixed module and non-module",
			in: []*bbinternal.Package{
				cmd("example.com/m/cmd/a", someModule),
				cmd("gopath.com/cmd/b", nil),
			},
			wantNamed: []string{"gopath.com/cmd/b"},
		},
		{
			// Every offending command is named, not just the first.
			name: "every module-less command is named",
			in: []*bbinternal.Package{
				cmd("z.com/cmd/z", nil),
				cmd("a.com/cmd/a", nil),
			},
			wantNamed: []string{"a.com/cmd/a", "z.com/cmd/z"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := checkModules(tt.in)
			if len(tt.wantNamed) == 0 {
				if err != nil {
					t.Fatalf("checkModules() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("checkModules() = nil, want an error")
			}
			if !errors.Is(err, ErrNoModule) {
				t.Errorf("checkModules() = %v, want it to wrap ErrNoModule", err)
			}
			for _, want := range tt.wantNamed {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("checkModules() = %q, want it to name %q", err, want)
				}
			}
		})
	}
}

// TestCheckModulesErrorIsActionable guards the part of the message that tells
// a user what to do instead. Rejecting GOPATH mode without naming its
// replacement would strand anyone whose build just stopped working.
func TestCheckModulesErrorIsActionable(t *testing.T) {
	err := checkModules([]*bbinternal.Package{cmd("gopath.com/cmd/a", nil)})
	if err == nil {
		t.Fatal("checkModules() = nil, want an error")
	}
	for _, want := range []string{"GOPATH", "goanywhere"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("checkModules() = %q, want it to mention %q", err, want)
		}
	}
}

// namedCmd is a command as checkDuplicate sees it: an import path, and the
// base name the busybox would reach it by.
func namedCmd(pkgPath string) *bbinternal.Package {
	return &bbinternal.Package{
		Name: path.Base(pkgPath),
		Pkg:  &packages.Package{PkgPath: pkgPath},
	}
}

func TestCheckDuplicate(t *testing.T) {
	t.Run("distinct names are fine", func(t *testing.T) {
		err := checkDuplicate([]*bbinternal.Package{
			namedCmd("m/cmds/ls"),
			namedCmd("m/cmds/ip"),
		})
		if err != nil {
			t.Errorf("checkDuplicate() = %v, want nil", err)
		}
	})

	t.Run("a name claimed twice is reported with both paths", func(t *testing.T) {
		err := checkDuplicate([]*bbinternal.Package{
			namedCmd("m/cmds/core/echo"),
			namedCmd("m/other/echo"),
		})
		if err == nil {
			t.Fatal("checkDuplicate() = nil, want an error")
		}
		for _, want := range []string{"echo", "m/cmds/core/echo", "m/other/echo"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("checkDuplicate() = %q, want it to mention %q", err, want)
			}
		}
	})

	// Returning on the first collision makes fixing a pattern that matched
	// hundreds of packages a sequence of rebuilds, each paying for a full
	// load to reveal the next one.
	t.Run("every collision is reported, not just the first", func(t *testing.T) {
		err := checkDuplicate([]*bbinternal.Package{
			namedCmd("m/cmds/core/echo"),
			namedCmd("m/other/echo"),
			namedCmd("m/cmds/core/man/gen"),
			namedCmd("m/cmds/exp/cbmem/gen"),
		})
		if err == nil {
			t.Fatal("checkDuplicate() = nil, want an error")
		}
		if !strings.Contains(err.Error(), "2 duplicate command names") {
			t.Errorf("checkDuplicate() = %q, want it to count both", err)
		}
		for _, want := range []string{"m/cmds/core/man/gen", "m/cmds/exp/cbmem/gen"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("checkDuplicate() = %q, want it to mention %q", err, want)
			}
		}
	})

	t.Run("the remedy is named", func(t *testing.T) {
		err := checkDuplicate([]*bbinternal.Package{
			namedCmd("m/a/echo"),
			namedCmd("m/b/echo"),
		})
		if err == nil {
			t.Fatal("checkDuplicate() = nil, want an error")
		}
		if !strings.Contains(err.Error(), "-m/a/echo") {
			t.Errorf("checkDuplicate() = %q, want it to show an exclusion to copy", err)
		}
	})
}

// dep is a package that imports others, for building a graph collectDeps can
// walk. packages.Visit follows Imports, so that is the only edge it needs.
func dep(pkgPath string, mod *packages.Module, imports ...*packages.Package) *packages.Package {
	p := &packages.Package{PkgPath: pkgPath, Module: mod, Imports: map[string]*packages.Package{}}
	for _, i := range imports {
		p.Imports[i.PkgPath] = i
	}
	return p
}

func TestCollectDeps(t *testing.T) {
	someModule := &packages.Module{Path: "example.com/m", Version: "v1.0.0", GoVersion: "1.21"}

	// A module path is not required to contain a dot. Only a module fetched
	// by its path has to look like a domain, so `module nodot` is legal,
	// and its packages have to be vendored like any other.
	dotless := &packages.Module{Path: "nodot", GoVersion: "1.21"}

	for _, tt := range []struct {
		name string
		in   *packages.Package
		want []string
	}{
		{
			name: "standard library is not vendored",
			in:   dep("example.com/m/cmd", someModule, dep("fmt", nil), dep("os", nil)),
			want: []string{"example.com/m/cmd"},
		},
		{
			// The case the dot test got wrong: every component of
			// these paths is dotless, so each was taken for
			// standard library and left out of the vendor
			// directory. The build then failed looking for
			// nodot/pkg/hello in GOROOT.
			name: "dotless module path is vendored",
			in:   dep("nodot/cmd/hi", dotless, dep("nodot/pkg/hello", dotless, dep("fmt", nil))),
			want: []string{"nodot/cmd/hi", "nodot/pkg/hello"},
		},
		{
			name: "dotted module path is still vendored",
			in: dep("example.com/m/cmd", someModule,
				dep("example.com/m/pkg", someModule),
				dep("fmt", nil)),
			want: []string{"example.com/m/cmd", "example.com/m/pkg"},
		},
		{
			// A standard library package that imports another one
			// must not drag it in.
			name: "transitive standard library is not vendored",
			in:   dep("example.com/m/cmd", someModule, dep("os", nil, dep("internal/abi", nil))),
			want: []string{"example.com/m/cmd"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, p := range collectDeps(tt.in) {
				got = append(got, p.PkgPath)
			}
			sort.Strings(got)
			sort.Strings(tt.want)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("collectDeps() = %v, want %v", got, tt.want)
			}
		})
	}
}
