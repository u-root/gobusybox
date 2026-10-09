// Copyright 2015-2024 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bb

import (
	"errors"
	"reflect"
	"testing"

	"golang.org/x/tools/go/packages"
)

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
		// Only main modules reach this function, so the case that
		// matters is a workspace member whose own path carries a major
		// version suffix.
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
			got, err := vendorManifest(tt.in)
			if err != nil {
				t.Fatalf("vendorManifest() = %v, want no error", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("vendorManifest() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

// TestVendorManifestErrors covers the cases a flat vendor directory cannot
// represent. Both used to be papered over with a synthetic single-package
// module, which silently attributed a package to a module it did not come
// from.
func TestVendorManifestErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   map[string]*packages.Package
		want error
	}{
		{
			// GOPATH mode reports no module at all. It is the only
			// mode in which one import path can exist at two
			// versions at once, which a flat vendor directory
			// cannot hold.
			name: "no module at all",
			in: map[string]*packages.Package{
				"example.com/m/a": pkg("example.com/m/a", nil),
			},
			want: ErrNoModule,
		},
		{
			// Every vendored package must sit under its module's
			// path, or the go tool rejects the vendor directory.
			name: "module path is not a prefix of the import path",
			in: map[string]*packages.Package{
				"other.com/p": pkg("other.com/p", &packages.Module{Path: "example.com/m", Version: "v1.0.0", GoVersion: "1.21"}),
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := vendorManifest(tt.in)
			if err == nil {
				t.Fatalf("vendorManifest() = %+v, want an error", got)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("vendorManifest() = %v, want %v", err, tt.want)
			}
		})
	}
}
