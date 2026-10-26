// Copyright 2018 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package findpkg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/u-root/gobusybox/src/pkg/golang"
	"github.com/u-root/uio/ulog/ulogtest"
	"golang.org/x/tools/go/packages"
)

type testCase struct {
	// name of the test case
	name string
	// envs to try it in (if unset, defaults to GO111MODULE=on)
	envs []*golang.Environ
	// wd sets the findpkg.Env.WorkingDirectory
	// WorkingDirectory is the directory used for module-enabled
	// `go list` lookups. The go.mod in this directory (or one of
	// its parents) is used to resolve package paths.
	wd string
	// GBB_PATH
	gbbPath []string
	// Input patterns
	in []string
	// Expected result from ResolveGlobs
	want []string
	// Expected result from NewPackages each packages' PkgPath
	wantPkgPath []string
	// Error expected?
	wantErr bool
	// If set, expected error.
	err error
}

func TestResolve(t *testing.T) {
	gbbmod, err := filepath.Abs("../../../")
	if err != nil {
		t.Fatalf("failure to set up test: %v", err)
	}
	gbbroot := filepath.Dir(gbbmod)

	moduleOnEnv := golang.Default(golang.WithGO111MODULE("on"))
	// TODO: re-enable when https://github.com/golang/go/issues/62114 is resolved.
	// noGoToolEnv := golang.Default(golang.WithGOROOT(t.TempDir()))

	if err := os.Mkdir("./test/resolvebroken", 0777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll("./test/resolvebroken") })
	if err := os.WriteFile("./test/resolvebroken/main.go", []byte("broken"), 0777); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir("./test/parsebroken", 0777); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll("./test/parsebroken") })
	if err := os.WriteFile("./test/parsebroken/main.go", []byte("package main\n\nimport \"fmt\""), 0777); err != nil {
		t.Fatal(err)
	}

	l := &ulogtest.Logger{TB: t}

	sharedTestCases := []testCase{
		// Nonexistent Package
		{
			name:    "fakepackage",
			in:      []string{"fakepackagename"},
			wantErr: true,
		},
		// Single package, file system path.
		{
			name: "fspath-single",
			in:   []string{filepath.Join(gbbmod, "cmd/makebb")},
			want: []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
		},
		// Single package, file system path, GBB_PATHS.
		{
			name:    "fspath-gbbpath-single",
			gbbPath: []string{gbbmod},
			in:      []string{"cmd/makebb"},
			want:    []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
		},
		// Single package, Go package path.
		{
			name: "pkgpath-single",
			in:   []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
			want: []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
		},
		// Several packages, globbed file system path.
		{
			name: "fspath-glob",
			in:   []string{filepath.Join(gbbmod, "cmd/g*")},
			want: []string{"github.com/u-root/gobusybox/src/cmd/gencmddeps", "github.com/u-root/gobusybox/src/cmd/goanywhere", "github.com/u-root/gobusybox/src/cmd/gomodbb"},
		},
		// Several packages, globbed Go package path.
		{
			name: "pkgpath-glob",
			in:   []string{"github.com/u-root/gobusybox/src/cmd/g*"},
			want: []string{"github.com/u-root/gobusybox/src/cmd/gencmddeps", "github.com/u-root/gobusybox/src/cmd/goanywhere", "github.com/u-root/gobusybox/src/cmd/gomodbb"},
		},
		// Globbed file system path of non-existent packages.
		{
			name:    "fspath-glob-doesnotexist",
			in:      []string{filepath.Join(gbbmod, "cmd/makeq*")},
			wantErr: true,
			err:     errNoMatch,
		},
		// Globbed package path of non-existent packages.
		{
			name:    "pkgpath-glob-doesnotexist",
			in:      []string{"github.com/u-root/gobusybox/src/cmd/makeq*"},
			wantErr: true,
			err:     errNoMatch,
		},
		// Two packages (file system paths), one excluded by build constraints.
		{
			name: "fspath-log-buildconstrained",
			in:   []string{"./test/buildconstraint", filepath.Join(gbbmod, "cmd/makebb")},
			want: []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
		},
		// Two packages (Go package paths), one excluded by build constraints.
		{
			name: "pkgpath-log-buildconstrained",
			in:   []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/buildconstraint", "github.com/u-root/gobusybox/src/cmd/makebb"},
			want: []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
		},
		// Package excluded by build constraints (file system paths).
		{
			name:    "fspath-log-buildconstrained-onlyone",
			in:      []string{"./test/buildconstraint"},
			err:     errNoMatch,
			wantErr: true,
		},
		// Package excluded by build constraints (Go package paths).
		{
			name:    "pkgpath-log-buildconstrained-onlyone",
			in:      []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/buildconstraint"},
			err:     errNoMatch,
			wantErr: true,
		},
		// Go glob support (Go package path).
		{
			name: "pkgpath-go-glob",
			in:   []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/..."},
			want: []string{
				"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/echo",
				"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/foo",
			},
		},
		// Go glob support (relative Go package path).
		{
			name: "pkgpath-relative-go-glob",
			in:   []string{"./test/goglob/..."},
			want: []string{
				"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/echo",
				"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/foo",
			},
		},
		// Go glob support ("relative" Go package path, without ./ -- follows Go semantics).
		//
		// This is actually just a Go package path, and not interpreted
		// as a file system path by the Go lookup (because they must be
		// able to distinguish between "cmd/compile" and
		// "./cmd/compile").
		{
			name:    "pkgpath-relative-go-glob-broken",
			in:      []string{"test/goglob/..."},
			wantErr: true,
			err:     errNoMatch,
		},
		{
			name:    "fspath-empty-directory",
			in:      []string{"./test/empty"},
			wantErr: true,
		},
		{
			name:    "pkgpath-empty-directory",
			in:      []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/empty"},
			wantErr: true,
		},
		// resolvebroken is not compilable.
		{
			name:    "fspath-broken-go",
			in:      []string{"./test/resolvebroken"},
			wantErr: true,
		},
		{
			name:    "pkgpath-broken-go",
			in:      []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/resolvebroken"},
			wantErr: true,
		},
		// Contains test/resolvebroken which is not compilable.
		{
			name:    "fspath-glob-with-errors",
			in:      []string{"./test/*"},
			wantErr: true,
		},
		{
			name:    "pkgpath-glob-with-errors",
			in:      []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/*"},
			wantErr: true,
		},
		// Multi module resolution, package path. (GO111MODULE=on only)
		//
		// Unless we put u-root and p9 in GOPATH in the local version
		// of this test, this is an ON only test.
		{
			name: "pkgpath-multi-module",
			envs: []*golang.Environ{moduleOnEnv},
			wd:   filepath.Join(gbbroot, "test/resolve-modules"),
			in: []string{
				"github.com/u-root/u-root/cmds/core/init",
				"github.com/u-root/u-root/cmds/core/ip",
				"github.com/u-root/u-root/cmds/core/dhclient",
				"github.com/hugelgupf/p9/cmd/p9ufs",
			},
			want: []string{
				"github.com/hugelgupf/p9/cmd/p9ufs",
				"github.com/u-root/u-root/cmds/core/dhclient",
				"github.com/u-root/u-root/cmds/core/init",
				"github.com/u-root/u-root/cmds/core/ip",
			},
		},
		// Shell expansions.
		{
			name: "pkgpath-shell-expansion",
			envs: []*golang.Environ{moduleOnEnv},
			wd:   filepath.Join(gbbroot, "test/resolve-modules"),
			in: []string{
				"github.com/u-root/u-root/cmds/core/{init,ip,dhclient}",
			},
			want: []string{
				"github.com/u-root/u-root/cmds/core/dhclient",
				"github.com/u-root/u-root/cmds/core/init",
				"github.com/u-root/u-root/cmds/core/ip",
			},
		},
		// Exclusion, single package, file system path.
		{
			name: "fspath-exclusion",
			in:   []string{"./test/goglob/*", "-test/goglob/echo"},
			want: []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/foo"},
		},
		// Exclusion, single package, Go package path.
		{
			name: "pkgpath-exclusion",
			in:   []string{"./test/goglob/...", "-github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/echo"},
			want: []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/foo"},
		},
		// Exclusion, single package, mixed.
		{
			name: "path-exclusion",
			in:   []string{"./test/goglob/...", "-test/goglob/echo"},
			want: []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/foo"},
		},
		// Globs in exclusions should work.
		//
		// Unless we put u-root and p9 in GOPATH in the local version
		// of this test, this is an ON only test.
		{
			name: "pkgpath-multi-module-exclusion-glob",
			envs: []*golang.Environ{moduleOnEnv},
			wd:   filepath.Join(gbbroot, "test/resolve-modules"),
			in: []string{
				"github.com/u-root/u-root/cmds/core/init",
				"github.com/u-root/u-root/cmds/core/ip",
				"github.com/u-root/u-root/cmds/core/yes",
				"github.com/hugelgupf/p9/cmd/p9ufs",
				"-github.com/u-root/u-root/cmds/core/y*",
			},
			want: []string{
				"github.com/hugelgupf/p9/cmd/p9ufs",
				"github.com/u-root/u-root/cmds/core/init",
				"github.com/u-root/u-root/cmds/core/ip",
			},
		},
		// File system path. Not a directory.
		{
			name:    "fspath-not-a-directory",
			in:      []string{"./bb_test.go"},
			wantErr: true,
			err:     errNoMatch,
		},
		// Some error cases where $GOROOT/bin/go is unavailable, so packages.Load fails.
		/*
			TODO: re-enable when https://github.com/golang/go/issues/62114 is resolved.
			{
				name:    "fspath-load-fails",
				envs:    []*golang.Environ{noGoToolEnv},
				in:      []string{"./test/goglob/*"},
				wantErr: true,
			},
			{
				name:    "pkgpath-batched-load-fails",
				envs:    []*golang.Environ{noGoToolEnv},
				in:      []string{"./test/goglob/..."},
				wantErr: true,
			},
			{
				name:    "pkgpath-glob-load-fails",
				envs:    []*golang.Environ{noGoToolEnv},
				in:      []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/*"},
				wantErr: true,
			},
		*/
	}

	for _, tc := range sharedTestCases {
		envs := []*golang.Environ{moduleOnEnv}
		if tc.envs != nil {
			envs = tc.envs
		}
		for _, env := range envs {
			env = env.Copy(golang.WithWorkingDir(tc.wd))
			t.Run(fmt.Sprintf("ResolveGlobs-GO111MODULE=%s-%s", env.GO111MODULE, tc.name), func(t *testing.T) {
				e := Env{
					GBBPath: tc.gbbPath,
				}
				out, err := ResolveGlobs(l, env, e, tc.in)
				if tc.err != nil && !errors.Is(err, tc.err) {
					t.Errorf("ResolveGlobs(%v, %v) = %v, want %v", e, tc.in, err, tc.err)
				}
				if (err != nil) != tc.wantErr {
					t.Errorf("ResolveGlobs(%v, %v) = (%v, %v), wantErr is %t", e, tc.in, out, err, tc.wantErr)
				}
				if !reflect.DeepEqual(out, tc.want) {
					t.Errorf("ResolveGlobs(%v, %v) = %#v; want %#v", e, tc.in, out, tc.want)
				}
			})
		}
	}

	newPkgTests := append(sharedTestCases, testCase{
		name:    "fspath-parse-broken",
		in:      []string{"./test/parsebroken"},
		wantErr: true,
	}, testCase{
		name:    "pkgpath-parse-broken",
		in:      []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/parsebroken"},
		wantErr: true,
	})
	for _, tc := range newPkgTests {
		envs := []*golang.Environ{moduleOnEnv}
		if tc.envs != nil {
			envs = tc.envs
		}
		for _, env := range envs {
			env = env.Copy(golang.WithWorkingDir(tc.wd))
			t.Run(fmt.Sprintf("NewPackage-GO111MODULE=%s-%s", env.GO111MODULE, tc.name), func(t *testing.T) {
				e := Env{
					GBBPath: tc.gbbPath,
				}
				out, err := NewPackages(l, env, e, tc.in...)
				if tc.err != nil && !errors.Is(err, tc.err) {
					t.Errorf("NewPackages(%v, %v) = %v, want %v", e, tc.in, err, tc.err)
				}
				if (err != nil) != tc.wantErr {
					t.Errorf("NewPackages(%v, %v) = (%v, %v), wantErr is %t", e, tc.in, out, err, tc.wantErr)
				}

				var pkgPaths []string
				for _, p := range out {
					pkgPaths = append(pkgPaths, p.Pkg.PkgPath)
				}
				sort.Strings(pkgPaths)
				if !reflect.DeepEqual(pkgPaths, tc.want) {
					t.Errorf("NewPackages(%v, %v) = %v; want %v", e, tc.in, out, tc.want)
				}
			})

		}
	}
}

func TestDefaultEnv(t *testing.T) {
	for _, tc := range []struct {
		GBB_PATH     string
		UROOT_SOURCE string
		s            string
		want         Env
	}{
		{
			GBB_PATH:     "foo:bar",
			UROOT_SOURCE: "./foo",
			s:            "GBB_PATH=foo:bar UROOT_SOURCE=./foo",
			want: Env{
				GBBPath:     []string{"foo", "bar"},
				URootSource: "./foo",
			},
		},
		{
			GBB_PATH: "foo",
			s:        "GBB_PATH=foo UROOT_SOURCE=",
			want: Env{
				GBBPath: []string{"foo"},
			},
		},
		{
			s:    "GBB_PATH= UROOT_SOURCE=",
			want: Env{},
		},
	} {
		t.Run(tc.s, func(t *testing.T) {
			os.Setenv("GBB_PATH", tc.GBB_PATH)
			os.Setenv("UROOT_SOURCE", tc.UROOT_SOURCE)
			e := DefaultEnv()
			if !reflect.DeepEqual(e, tc.want) {
				t.Errorf("Env = %#v, want %#v", e, tc.want)
			}
			if e.String() != tc.s {
				t.Errorf("Env.String() = %v, want %v", e, tc.s)
			}
		})
	}
}

func TestModules(t *testing.T) {
	dir := t.TempDir()

	_ = os.MkdirAll(filepath.Join(dir, "mod1/cmd/cmd1"), 0755)
	_ = os.MkdirAll(filepath.Join(dir, "mod1/cmd/cmd2"), 0755)
	_ = os.MkdirAll(filepath.Join(dir, "mod1/nestedmod1/cmd/cmd5"), 0755)
	_ = os.MkdirAll(filepath.Join(dir, "mod1/nestedmod2/cmd/cmd6"), 0755)
	_ = os.MkdirAll(filepath.Join(dir, "mod2/cmd/foo3"), 0755)
	_ = os.MkdirAll(filepath.Join(dir, "mod2/cmd/foo4"), 0755)
	_ = os.MkdirAll(filepath.Join(dir, "nomod/cmd/cmd7"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "mod1/go.mod"), nil, 0644)
	_ = os.WriteFile(filepath.Join(dir, "mod1/nestedmod1/go.mod"), nil, 0644)
	_ = os.WriteFile(filepath.Join(dir, "mod1/nestedmod2/go.mod"), nil, 0644)
	_ = os.WriteFile(filepath.Join(dir, "mod2/go.mod"), nil, 0644)

	paths := []string{
		filepath.Join(dir, "mod1/cmd/cmd1"),
		filepath.Join(dir, "mod1/cmd/cmd2"),
		filepath.Join(dir, "mod1/nestedmod1/cmd/cmd5"),
		filepath.Join(dir, "mod1/nestedmod2/cmd/cmd6"),
		filepath.Join(dir, "mod2/cmd/foo3"),
		filepath.Join(dir, "mod2/cmd/foo4"),
		filepath.Join(dir, "nomod/cmd/cmd7"),
	}
	mods, noModulePkgs := Modules(paths)

	want := map[string][]string{
		filepath.Join(dir, "mod1"): {
			filepath.Join(dir, "mod1/cmd/cmd1"),
			filepath.Join(dir, "mod1/cmd/cmd2"),
		},
		filepath.Join(dir, "mod1/nestedmod1"): {
			filepath.Join(dir, "mod1/nestedmod1/cmd/cmd5"),
		},
		filepath.Join(dir, "mod1/nestedmod2"): {
			filepath.Join(dir, "mod1/nestedmod2/cmd/cmd6"),
		},
		filepath.Join(dir, "mod2"): {
			filepath.Join(dir, "mod2/cmd/foo3"),
			filepath.Join(dir, "mod2/cmd/foo4"),
		},
	}
	if !reflect.DeepEqual(mods, want) {
		t.Errorf("modules() = %v, want %v", mods, want)
	}
	wantNoModule := []string{
		filepath.Join(dir, "nomod/cmd/cmd7"),
	}
	if !reflect.DeepEqual(noModulePkgs, wantNoModule) {
		t.Errorf("modules() no module pkgs = %v, want %v", noModulePkgs, wantNoModule)
	}

	wantG := []string{
		filepath.Join(dir, "mod1/cmd/cmd1"),
		filepath.Join(dir, "mod2/cmd/foo3"),
	}
	e := Env{
		GBBPath: []string{filepath.Join(dir, "mod1"), filepath.Join(dir, "mod2")},
	}
	if got := GlobPaths(&ulogtest.Logger{TB: t}, e, `cmd/cmd*`, "cmd/foo3", "-cmd/cmd2"); !reflect.DeepEqual(got, wantG) {
		t.Errorf("GlobPaths = %v, want %v", got, wantG)
	}
}

// modPkg is a package in a module, as the cheap lookup returns it. Only the
// fields checkToolchainVersion reads are set.
func modPkg(pkgPath, modPath, goDirective string) *packages.Package {
	return &packages.Package{
		PkgPath: pkgPath,
		Module: &packages.Module{
			Path:      modPath,
			GoVersion: goDirective,
		},
	}
}

func TestCheckToolchainVersion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		toolchain string
		pkgs      []*packages.Package
		// Substrings the message must contain. Empty means no error.
		wantContains []string
	}{
		{
			name:      "no packages",
			toolchain: "go1.27.0",
		},
		{
			name:      "package outside any module",
			toolchain: "go1.27.0",
			pkgs:      []*packages.Package{{PkgPath: "cmd/hello"}},
		},
		{
			name:      "module without a go directive",
			toolchain: "go1.27.0",
			pkgs:      []*packages.Package{modPkg("example.com/m/hello", "example.com/m", "")},
		},
		{
			name:      "module older than the toolchain",
			toolchain: "go1.27.0",
			pkgs:      []*packages.Package{modPkg("example.com/m/hello", "example.com/m", "1.21")},
		},
		{
			name:      "module equal to the toolchain",
			toolchain: "go1.27.0",
			pkgs:      []*packages.Package{modPkg("example.com/m/hello", "example.com/m", "1.27.0")},
		},
		{
			// Measured: a tool built with go1.26.6 type-checks a
			// module declaring go 1.26.9 without complaint,
			// because go/types compares language versions. A
			// full-version comparison would refuse a build that
			// works.
			name:      "module newer only in its patch release",
			toolchain: "go1.27.0",
			pkgs:      []*packages.Package{modPkg("example.com/m/hello", "example.com/m", "1.27.4")},
		},
		{
			name:      "toolchain is not a Go version",
			toolchain: "devel +abcdef1234 Mon Jan 1 00:00:00 2029 -0800",
			pkgs:      []*packages.Package{modPkg("example.com/m/hello", "example.com/m", "1.99")},
		},
		{
			name:         "module newer than the toolchain",
			toolchain:    "go1.26.9",
			pkgs:         []*packages.Package{modPkg("example.com/m/hello", "example.com/m", "1.27.0")},
			wantContains: []string{"makebb", "go1.26", "example.com/m", "go1.27"},
		},
		{
			name:         "release candidate newer than the toolchain",
			toolchain:    "go1.26.9",
			pkgs:         []*packages.Package{modPkg("example.com/m/hello", "example.com/m", "1.27rc1")},
			wantContains: []string{"example.com/m", "go1.27"},
		},
		{
			// Rebuilding to satisfy the highest requirement
			// satisfies every other, so that is the one to name.
			name:      "the highest requirement is the one reported",
			toolchain: "go1.26.9",
			pkgs: []*packages.Package{
				modPkg("example.com/a/hello", "example.com/a", "1.27.0"),
				modPkg("example.com/c/hello", "example.com/c", "1.30.0"),
				modPkg("example.com/b/hello", "example.com/b", "1.29.0"),
			},
			wantContains: []string{"example.com/c", "go1.30"},
		},
		{
			// Load order is not something the user controls, so
			// the message must not depend on it.
			name:      "ties go to the lowest module path",
			toolchain: "go1.26.9",
			// The lowest path sits in the middle deliberately.
			// With it first, keeping the first tied module passes
			// without comparing paths at all; with it last,
			// keeping the last one does. Only a list it does not
			// bound distinguishes lowest-wins from either.
			pkgs: []*packages.Package{
				modPkg("example.com/z/hello", "example.com/z", "1.27.0"),
				modPkg("example.com/a/hello", "example.com/a", "1.27.0"),
				modPkg("example.com/m/hello", "example.com/m", "1.27.0"),
			},
			wantContains: []string{"example.com/a", "go1.27"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkToolchainVersion("makebb", tc.toolchain, tc.pkgs)
			if len(tc.wantContains) == 0 {
				if err != nil {
					t.Fatalf("checkToolchainVersion = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("checkToolchainVersion = nil, want an error")
			}
			if !errors.Is(err, errToolchainTooOld) {
				t.Errorf("checkToolchainVersion = %v, want errToolchainTooOld", err)
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("checkToolchainVersion = %q, want it to mention %q", err, want)
				}
			}
			// The misleading message this check exists to
			// pre-empt calls the too-old program "the
			// application". Ours must name it.
			if strings.Contains(err.Error(), "application") {
				t.Errorf("checkToolchainVersion = %q, want it to name the program rather than say \"application\"", err)
			}
		})
	}
}

// TestLookupPkgNameAndFilesLoadsModule guards the half checkToolchainVersion
// cannot see. Without packages.NeedModule the cheap lookup leaves Module nil,
// every package is skipped, and the check silently never fires again.
func TestLookupPkgNameAndFilesLoadsModule(t *testing.T) {
	env := golang.Default(golang.WithGO111MODULE("on"))
	pkgs, err := lookupPkgNameAndFiles(env, "./test/goglob/...")
	if err != nil {
		t.Fatalf("lookupPkgNameAndFiles = %v, want nil", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("lookupPkgNameAndFiles returned no packages, so this guards nothing")
	}
	for _, p := range pkgs {
		if p.Module == nil {
			t.Errorf("package %s has no module; checkToolchainVersion cannot see it", p.PkgPath)
			continue
		}
		if p.Module.GoVersion == "" {
			t.Errorf("package %s module %s has no go directive; checkToolchainVersion cannot see it", p.PkgPath, p.Module.Path)
		}
	}
}

func TestProgName(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "a path", args: []string{"/usr/local/bin/makebb"}, want: "makebb"},
		{name: "a bare name", args: []string{"gomodbb"}, want: "gomodbb"},
		{name: "empty", args: []string{""}, want: "this program"},
		{name: "absent", args: nil, want: "this program"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := os.Args
			t.Cleanup(func() { os.Args = saved })
			os.Args = tc.args
			if got := progName(); got != tc.want {
				t.Errorf("progName() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveGlobsRefusesATooNewModule drives the real lookup path, which is
// the half the table test above cannot reach: that the check is called at all,
// that the go directive survives the cheap load to get there, and that the
// message reaches the caller instead of being wrapped in one of the lookup
// failures it sits next to.
//
// The toolchain running this test is at least as new as this module by
// construction, so the version it was built with is faked rather than the
// fixture's go directive raised -- a go directive above the go binary on PATH
// fails in `go list`, which is a different and already clear error.
func TestResolveGlobsRefusesATooNewModule(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pattern string
	}{
		// Resolved by the batched branch of
		// lookupCompilablePkgsWithGlob...
		{name: "a plain pattern", pattern: "./test/goglob/..."},
		// ...and by the glob branch, whose own failure message
		// ("is neither package or path/glob") must not swallow this
		// one.
		{name: "a package path glob", pattern: "github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := goToolchainVersion
			t.Cleanup(func() { goToolchainVersion = saved })
			goToolchainVersion = func() string { return "go1.2.3" }

			_, err := ResolveGlobs(&ulogtest.Logger{TB: t}, golang.Default(golang.WithGO111MODULE("on")), Env{}, []string{tc.pattern})
			if !errors.Is(err, errToolchainTooOld) {
				t.Fatalf("ResolveGlobs = %v, want errToolchainTooOld", err)
			}
			for _, want := range []string{"go1.2", "github.com/u-root/gobusybox/src", "rebuild"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("ResolveGlobs = %q, want it to mention %q", err, want)
				}
			}
		})
	}
}

// twoModuleWorkspace writes a Go workspace holding two modules: one
// declaring a go directive the faked toolchain below can read, and one
// declaring a directive it cannot.
//
// It is built here rather than committed under test/ because the point of it
// is the go directives, and a second go.mod inside this module's tree would
// have to be kept out of the way of `go build ./...` and `go mod tidy`. It
// needs no network: every import resolves to a `use` directive in the
// workspace.
func twoModuleWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(p, contents string) {
		t.Helper()
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.work", "go 1.24.0\n\nuse (\n\t./oldmod\n\t./newmod\n)\n")

	write("oldmod/go.mod", "module example.com/oldmod\n\ngo 1.21\n\nrequire example.com/newmod v0.0.0\n")
	write("oldmod/cmd/fine/main.go", "package main\n\nfunc main() {}\n")
	write("oldmod/cmd/importer/main.go", "package main\n\nimport \"example.com/newmod/pkg/lib\"\n\nfunc main() { lib.F() }\n")

	write("newmod/go.mod", "module example.com/newmod\n\ngo 1.24.0\n")
	write("newmod/cmd/toonew/main.go", "package main\n\nfunc main() {}\n")
	write("newmod/pkg/lib/lib.go", "package lib\n\nfunc F() {}\n")
	write("newmod/cmd/windowsonly/main.go", "//go:build windows\n\npackage main\n\nfunc main() {}\n")
	return root
}

// tooNewFor fakes the toolchain this program was built with, so that
// example.com/newmod is out of reach and example.com/oldmod is not.
func tooNewFor(t *testing.T) *golang.Environ {
	t.Helper()
	saved := goToolchainVersion
	t.Cleanup(func() { goToolchainVersion = saved })
	goToolchainVersion = func() string { return "go1.22.0" }
	return golang.Default(golang.WithGO111MODULE("on"), golang.WithWorkingDir(twoModuleWorkspace(t)))
}

// TestResolveGlobsJudgesOnlyTheCommandsItWillBuild pins where the check sits.
//
// The lookup it used to sit inside runs twice, once for the includes and once
// for the exclusions, and neither run can tell a command that is about to be
// built from one the user named in order to leave it out. It also ran ahead
// of checkEligibility, so a library or a build-constraint-excluded package
// swept up by a `...` pattern counted too. None of the three is compiled, so
// none of them can be refused for its module's go directive.
func TestResolveGlobsJudgesOnlyTheCommandsItWillBuild(t *testing.T) {
	for _, tc := range []struct {
		name     string
		patterns []string
		// Defaults to the one command outside the too-new module.
		want []string
	}{
		{
			// The include resolves the too-new command and the
			// exclusion takes it out again, so this is wrong
			// twice over if the check is in the wrong place:
			// once on the include set, which still holds it, and
			// once on the exclusion itself, which is resolved by
			// the same lookup.
			name:     "a module the user excluded",
			patterns: []string{"./oldmod/...", "./newmod/...", "-./newmod/..."},
			want:     []string{"example.com/oldmod/cmd/fine", "example.com/oldmod/cmd/importer"},
		},
		{
			name:     "a library, which is not a command",
			patterns: []string{"./oldmod/cmd/fine", "./newmod/pkg/lib"},
		},
		{
			name:     "a command whose build constraints exclude every file",
			patterns: []string{"./oldmod/cmd/fine", "./newmod/cmd/windowsonly"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := tooNewFor(t)
			paths, err := ResolveGlobs(&ulogtest.Logger{TB: t}, env, Env{}, tc.patterns)
			if err != nil {
				t.Fatalf("ResolveGlobs(%v) = %v, want nil: nothing named here is built out of the too-new module", tc.patterns, err)
			}
			want := tc.want
			if want == nil {
				want = []string{"example.com/oldmod/cmd/fine"}
			}
			if !reflect.DeepEqual(paths, want) {
				t.Errorf("ResolveGlobs(%v) = %v, want %v", tc.patterns, paths, want)
			}
		})
	}

	// The control, without which every case above is satisfied by a check
	// that never fires.
	t.Run("CONTROL: a command in the too-new module", func(t *testing.T) {
		env := tooNewFor(t)
		patterns := []string{"./oldmod/cmd/fine", "./newmod/cmd/toonew"}
		_, err := ResolveGlobs(&ulogtest.Logger{TB: t}, env, Env{}, patterns)
		if !errors.Is(err, errToolchainTooOld) {
			t.Fatalf("ResolveGlobs(%v) = %v, want errToolchainTooOld", patterns, err)
		}
		if !strings.Contains(err.Error(), "example.com/newmod") {
			t.Errorf("ResolveGlobs(%v) = %q, want it to name example.com/newmod", patterns, err)
		}
	})
}

// TestNewPackagesRefusesATooNewDependencyModule covers the half the cheap
// lookup cannot see: the named command's own module is old enough, and the
// directive that is out of reach belongs to a module it only gets to by
// import.
//
// addPkg is not the backstop it looks like. Measured with a go1.26-built
// loader on a command in a go1.21 module importing a module declaring
// go 1.27, go/packages puts both the refusal and its advice on the
// *dependency* and leaves the root command IllTyped with no errors at all --
// so the load succeeds, addPkg reads an empty Errors, and an ill-typed
// package goes on to be rewritten.
func TestNewPackagesRefusesATooNewDependencyModule(t *testing.T) {
	env := tooNewFor(t)
	pkgs, err := NewPackages(&ulogtest.Logger{TB: t}, env, Env{}, "./oldmod/cmd/importer")
	if !errors.Is(err, errToolchainTooOld) {
		t.Fatalf("NewPackages = (%v, %v), want errToolchainTooOld", pkgs, err)
	}
	// The module named must be the dependency, not the command's own.
	if !strings.Contains(err.Error(), "example.com/newmod") {
		t.Errorf("NewPackages = %q, want it to name example.com/newmod", err)
	}
	if strings.Contains(err.Error(), "example.com/oldmod") {
		t.Errorf("NewPackages = %q, want it to name the too-new module rather than the command's own", err)
	}
}

// TestNewPackagesAcceptsAnOldEnoughGraph is the control for the test above: a
// command whose whole import graph is within reach must still load. Without
// it, a check that refused everything would pass.
func TestNewPackagesAcceptsAnOldEnoughGraph(t *testing.T) {
	env := tooNewFor(t)
	pkgs, err := NewPackages(&ulogtest.Logger{TB: t}, env, Env{}, "./oldmod/cmd/fine")
	if err != nil {
		t.Fatalf("NewPackages = %v, want nil", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("NewPackages returned %d packages, want 1", len(pkgs))
	}
}
