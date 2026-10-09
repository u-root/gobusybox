// Copyright 2015-2024 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bb

import (
	"errors"
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
