// Copyright 2015-2024 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bb

import (
	"reflect"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestVendorPkgPath(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want string
	}{
		{"github.com/u-root/u-root/cmds/core/ip", "github.com/u-root/u-root/cmds/core/ip"},
		// A package reached through a GOPATH-style nested vendor
		// directory is reported under its vendored path, but is
		// imported -- and so must be written -- under its real one.
		{"github.com/u-root/u-root/vendor/golang.org/x/sys/unix", "golang.org/x/sys/unix"},
		// Only the last vendor segment delimits the real path.
		{"a.com/vendor/b.com/vendor/c.com/pkg", "c.com/pkg"},
		// "vendor" as an ordinary path element is not a delimiter.
		{"github.com/foo/vendor", "github.com/foo/vendor"},
		{"vendor/foo", "vendor/foo"},
	} {
		if got := vendorPkgPath(tt.in); got != tt.want {
			t.Errorf("vendorPkgPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSyntheticVersionFor(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want string
	}{
		{"github.com/foo/bar", "v0.0.0"},
		// A module path with a major version suffix may only be
		// required at that major version.
		{"github.com/foo/bar/v2", "v2.0.0"},
		{"github.com/foo/bar/v11", "v11.0.0"},
		// gopkg.in spells the suffix ".vN", not "/vN". Matching only
		// "/v" emitted "require gopkg.in/yaml.v2 v0.0.0", which the go
		// tool rejects outright:
		//
		//	version "v0.0.0" invalid: should be v2, not v0
		//
		// u-root pulls in gopkg.in/yaml.v2, so this broke every GOPATH
		// build, not some hypothetical one.
		{"gopkg.in/yaml.v2", "v2.0.0"},
		{"gopkg.in/yaml.v3", "v3.0.0"},
		// Unlike a bare path, gopkg.in's ".v1" demands v1, not v0.
		{"gopkg.in/check.v1", "v1.0.0"},
		{"gopkg.in/user/pkg.v4", "v4.0.0"},
		// /v0 and /v1 are not valid major version suffixes.
		{"github.com/foo/bar/v1", "v0.0.0"},
		{"github.com/foo/bar/v2/baz", "v0.0.0"},
		{"github.com/foo/version", "v0.0.0"},
	} {
		if got := syntheticVersionFor(tt.in); got != tt.want {
			t.Errorf("syntheticVersionFor(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func pkg(pkgPath string, mod *packages.Module) *packages.Package {
	return &packages.Package{PkgPath: pkgPath, Module: mod}
}

func TestVendorManifest(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   map[string]*packages.Package
		want []vendoredModule
	}{
		{
			name: "packages group under their module",
			in: map[string]*packages.Package{
				"example.com/m/a": pkg("example.com/m/a", &packages.Module{Path: "example.com/m", Version: "v1.2.3", GoVersion: "1.21"}),
				"example.com/m/b": pkg("example.com/m/b", &packages.Module{Path: "example.com/m", Version: "v1.2.3", GoVersion: "1.21"}),
			},
			want: []vendoredModule{{
				Path: "example.com/m", Version: "v1.2.3", GoVersion: "1.21",
				Packages: []string{"example.com/m/a", "example.com/m/b"},
			}},
		},
		{
			// A main module has no version of its own.
			name: "main module gets a synthetic version",
			in: map[string]*packages.Package{
				"example.com/m/a": pkg("example.com/m/a", &packages.Module{Path: "example.com/m", GoVersion: "1.21"}),
			},
			want: []vendoredModule{{
				Path: "example.com/m", Version: "v0.0.0", GoVersion: "1.21",
				Packages: []string{"example.com/m/a"},
			}},
		},
		{
			// GOPATH mode reports no module at all.
			name: "no module becomes a synthetic single-package module",
			in: map[string]*packages.Package{
				"example.com/m/a": pkg("example.com/m/a", nil),
			},
			want: []vendoredModule{{
				Path: "example.com/m/a", Version: "v0.0.0", GoVersion: "1.25",
				Packages: []string{"example.com/m/a"},
			}},
		},
		{
			// A module with no go directive is go1.16 by definition.
			name: "missing go directive",
			in: map[string]*packages.Package{
				"example.com/m/a": pkg("example.com/m/a", &packages.Module{Path: "example.com/m", Version: "v1.0.0"}),
			},
			want: []vendoredModule{{
				Path: "example.com/m", Version: "v1.0.0", GoVersion: "1.16",
				Packages: []string{"example.com/m/a"},
			}},
		},
		{
			// An un-vendored package no longer belongs to the
			// module go/packages reported it under, so attributing
			// it there would put it outside that module's path.
			name: "un-vendored package is detached from its reported module",
			in: map[string]*packages.Package{
				"golang.org/x/sys/unix": pkg("u.com/r/vendor/golang.org/x/sys/unix", &packages.Module{Path: "u.com/r", Version: "v1.0.0", GoVersion: "1.21"}),
			},
			want: []vendoredModule{{
				Path: "golang.org/x/sys/unix", Version: "v0.0.0", GoVersion: "1.25",
				Packages: []string{"golang.org/x/sys/unix"},
			}},
		},
		{
			// Every vendored package must sit under its module's
			// path, or the go tool rejects the vendor directory.
			name: "module that is not a prefix is not used",
			in: map[string]*packages.Package{
				"other.com/p": pkg("other.com/p", &packages.Module{Path: "example.com/m", Version: "v1.0.0", GoVersion: "1.21"}),
			},
			want: []vendoredModule{{
				Path: "other.com/p", Version: "v0.0.0", GoVersion: "1.25",
				Packages: []string{"other.com/p"},
			}},
		},
		{
			name: "modules are sorted",
			in: map[string]*packages.Package{
				"z.com/p": pkg("z.com/p", &packages.Module{Path: "z.com", Version: "v1.0.0", GoVersion: "1.21"}),
				"a.com/p": pkg("a.com/p", &packages.Module{Path: "a.com", Version: "v1.0.0", GoVersion: "1.21"}),
			},
			want: []vendoredModule{
				{Path: "a.com", Version: "v1.0.0", GoVersion: "1.21", Packages: []string{"a.com/p"}},
				{Path: "z.com", Version: "v1.0.0", GoVersion: "1.21", Packages: []string{"z.com/p"}},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := vendorManifest(tt.in, "1.25")
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("vendorManifest() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}
