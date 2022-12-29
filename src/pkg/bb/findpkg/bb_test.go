// Copyright 2018 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package findpkg

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/u-root/gobusybox/src/pkg/golang"
	"github.com/u-root/uio/ulog/ulogtest"
)

var (
	urootSource = flag.String("uroot-source", "", "Directory path to u-root source location")
)

func TestModules(t *testing.T) {
	dir, err := ioutil.TempDir("", "test-modules-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	os.MkdirAll(filepath.Join(dir, "mod1/cmd/cmd1"), 0755)
	os.MkdirAll(filepath.Join(dir, "mod1/cmd/cmd2"), 0755)
	os.MkdirAll(filepath.Join(dir, "mod1/nestedmod1/cmd/cmd5"), 0755)
	os.MkdirAll(filepath.Join(dir, "mod1/nestedmod2/cmd/cmd6"), 0755)
	os.MkdirAll(filepath.Join(dir, "mod2/cmd/cmd3"), 0755)
	os.MkdirAll(filepath.Join(dir, "mod2/cmd/cmd4"), 0755)
	os.MkdirAll(filepath.Join(dir, "nomod/cmd/cmd7"), 0755)
	ioutil.WriteFile(filepath.Join(dir, "mod1/go.mod"), nil, 0644)
	ioutil.WriteFile(filepath.Join(dir, "mod1/nestedmod1/go.mod"), nil, 0644)
	ioutil.WriteFile(filepath.Join(dir, "mod1/nestedmod2/go.mod"), nil, 0644)
	ioutil.WriteFile(filepath.Join(dir, "mod2/go.mod"), nil, 0644)

	paths := []string{
		filepath.Join(dir, "mod1/cmd/cmd1"),
		filepath.Join(dir, "mod1/cmd/cmd2"),
		filepath.Join(dir, "mod1/nestedmod1/cmd/cmd5"),
		filepath.Join(dir, "mod1/nestedmod2/cmd/cmd6"),
		filepath.Join(dir, "mod2/cmd/cmd3"),
		filepath.Join(dir, "mod2/cmd/cmd4"),
		filepath.Join(dir, "nomod/cmd/cmd7"),
	}
	mods, noModulePkgs := modules(paths)

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
			filepath.Join(dir, "mod2/cmd/cmd3"),
			filepath.Join(dir, "mod2/cmd/cmd4"),
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
}

type testCase struct {
	// name of the test case
	name string
	// envs to try it in (if unset, default will be GO111MODULE=on and off)
	envs []golang.Environ
	// wd sets the findpkg.Env.WorkingDirectory
	// WorkingDirectory is the directory used for module-enabled
	// `go list` lookups. The go.mod in this directory (or one of
	// its parents) is used to resolve package paths.
	wd string
	// UROOT_SOURCE
	urootSource string
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
	if *urootSource == "" {
		t.Fatalf("Test must be started with -uroot-source= set to local path to u-root file system directory")
	}
	urootSrc, err := filepath.Abs(*urootSource)
	if err != nil {
		t.Fatal(err)
	}

	gbbmod, err := filepath.Abs("../../../")
	if err != nil {
		t.Fatalf("failure to set up test: %v", err)
	}
	gbbroot := filepath.Dir(gbbmod)

	moduleOffEnv := golang.Default()
	moduleOffEnv.GO111MODULE = "off"

	moduleOnEnv := golang.Default()
	moduleOnEnv.GO111MODULE = "on"

	noGoToolEnv := golang.Default()
	noGoToolEnv.GOROOT = t.TempDir()

	if err := os.Mkdir("./test/resolvebroken", 0777); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll("./test/resolvebroken")
	if err := ioutil.WriteFile("./test/resolvebroken/main.go", []byte("broken"), 0777); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir("./test/parsebroken", 0777); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll("./test/parsebroken")
	if err := ioutil.WriteFile("./test/parsebroken/main.go", []byte("package main\n\nimport \"fmt\""), 0777); err != nil {
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
			name:        "fspath-single",
			in:          []string{filepath.Join(gbbmod, "cmd/makebb")},
			want:        []string{filepath.Join(gbbmod, "cmd/makebb")},
			wantPkgPath: []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
		},
		// Single package, file system path, GBB_PATHS.
		{
			name:        "fspath-gbbpath-single",
			gbbPath:     []string{gbbmod},
			in:          []string{"cmd/makebb"},
			want:        []string{filepath.Join(gbbmod, "cmd/makebb")},
			wantPkgPath: []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
		},
		// Single package, Go package path.
		{
			name:        "pkgpath-single",
			in:          []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
			want:        []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
			wantPkgPath: []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
		},
		// Two packages, globbed file system path.
		{
			name:        "fspath-glob",
			in:          []string{filepath.Join(gbbmod, "cmd/make*")},
			want:        []string{filepath.Join(gbbmod, "cmd/makebb"), filepath.Join(gbbmod, "cmd/makebbmain")},
			wantPkgPath: []string{"github.com/u-root/gobusybox/src/cmd/makebb", "github.com/u-root/gobusybox/src/cmd/makebbmain"},
		},
		// Two packages, globbed Go package path.
		{
			name:        "pkgpath-glob",
			in:          []string{"github.com/u-root/gobusybox/src/cmd/make*"},
			want:        []string{"github.com/u-root/gobusybox/src/cmd/makebb", "github.com/u-root/gobusybox/src/cmd/makebbmain"},
			wantPkgPath: []string{"github.com/u-root/gobusybox/src/cmd/makebb", "github.com/u-root/gobusybox/src/cmd/makebbmain"},
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
			name:        "fspath-log-buildconstrained",
			in:          []string{"./test/buildconstraint", filepath.Join(gbbmod, "cmd/makebb")},
			want:        []string{filepath.Join(gbbmod, "cmd/makebb")},
			wantPkgPath: []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
		},
		// Two packages (Go package paths), one excluded by build constraints.
		{
			name:        "pkgpath-log-buildconstrained",
			in:          []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/buildconstraint", "github.com/u-root/gobusybox/src/cmd/makebb"},
			want:        []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
			wantPkgPath: []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
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
			wantPkgPath: []string{
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
			wantPkgPath: []string{
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
		// Finding packages in more than 1 module, file system paths.
		{
			name: "fspath-multi-module",
			in: []string{
				filepath.Join(gbbmod, "cmd/makebb"),
				filepath.Join(gbbroot, "test/normaldeps/mod1/cmd/getppid"),
			},
			want: []string{
				filepath.Join(gbbmod, "cmd/makebb"),
				filepath.Join(gbbroot, "test/normaldeps/mod1/cmd/getppid"),
			},
			wantPkgPath: []string{
				"github.com/u-root/gobusybox/src/cmd/makebb",
				"github.com/u-root/gobusybox/test/normaldeps/mod1/cmd/getppid",
			},
		},
		// Finding packages in more than 1 module, file system paths, GBB_PATHS support.
		{
			name:    "fspath-gbbpath-multi-module",
			gbbPath: []string{gbbmod, gbbroot},
			in: []string{
				"cmd/makebb",
				"test/normaldeps/mod1/cmd/getppid",
			},
			want: []string{
				filepath.Join(gbbmod, "cmd/makebb"),
				filepath.Join(gbbroot, "test/normaldeps/mod1/cmd/getppid"),
			},
			wantPkgPath: []string{
				"github.com/u-root/gobusybox/src/cmd/makebb",
				"github.com/u-root/gobusybox/test/normaldeps/mod1/cmd/getppid",
			},
		},
		// Multi module resolution, package path. (GO111MODULE=on only)
		//
		// Unless we put u-root and p9 in GOPATH in the local version
		// of this test, this is an ON only test.
		{
			name: "pkgpath-multi-module",
			envs: []golang.Environ{moduleOnEnv},
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
			wantPkgPath: []string{
				"github.com/hugelgupf/p9/cmd/p9ufs",
				"github.com/u-root/u-root/cmds/core/dhclient",
				"github.com/u-root/u-root/cmds/core/init",
				"github.com/u-root/u-root/cmds/core/ip",
			},
		},
		// Exclusion, single package, file system path.
		{
			name:        "fspath-exclusion",
			in:          []string{"./test/goglob/*", "-test/goglob/echo"},
			want:        []string{filepath.Join(gbbmod, "pkg/bb/findpkg/test/goglob/foo")},
			wantPkgPath: []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/foo"},
		},
		// Exclusion, single package, Go package path.
		{
			name:        "pkgpath-exclusion",
			in:          []string{"./test/goglob/...", "-github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/echo"},
			want:        []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/foo"},
			wantPkgPath: []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/foo"},
		},
		// Globs in exclusions should work.
		//
		// Unless we put u-root and p9 in GOPATH in the local version
		// of this test, this is an ON only test.
		{
			name: "pkgpath-multi-module-exclusion-glob",
			envs: []golang.Environ{moduleOnEnv},
			wd:   filepath.Join(gbbroot, "test/resolve-modules"),
			in: []string{
				"github.com/u-root/u-root/cmds/core/init",
				"github.com/u-root/u-root/cmds/core/ip",
				"github.com/u-root/u-root/cmds/core/dhclient",
				"github.com/hugelgupf/p9/cmd/p9ufs",
				"-github.com/u-root/u-root/cmds/core/i*",
			},
			want: []string{
				"github.com/hugelgupf/p9/cmd/p9ufs",
				"github.com/u-root/u-root/cmds/core/dhclient",
			},
			wantPkgPath: []string{
				"github.com/hugelgupf/p9/cmd/p9ufs",
				"github.com/u-root/u-root/cmds/core/dhclient",
			},
		},
		{
			name:        "fspath-nomodule",
			envs:        []golang.Environ{moduleOffEnv},
			in:          []string{filepath.Join(gbbroot, "vendortest/cmd/dmesg")},
			want:        []string{filepath.Join(gbbroot, "vendortest/cmd/dmesg")},
			wantPkgPath: []string{"github.com/u-root/gobusybox/vendortest/cmd/dmesg"},
		},
		{
			name:        "pkgpath-nomodule",
			envs:        []golang.Environ{moduleOffEnv},
			in:          []string{"github.com/u-root/gobusybox/vendortest/cmd/dmesg"},
			want:        []string{"github.com/u-root/gobusybox/vendortest/cmd/dmesg"},
			wantPkgPath: []string{"github.com/u-root/gobusybox/vendortest/cmd/dmesg"},
		},
		// File system path. Not a directory.
		{
			name:    "fspath-not-a-directory",
			in:      []string{"./bb_test.go"},
			wantErr: true,
			err:     errNoMatch,
		},
		// Some error cases where $GOROOT/bin/go is unavailable, so packages.Load fails.
		{
			name:    "fspath-load-fails",
			envs:    []golang.Environ{noGoToolEnv},
			in:      []string{"./test/goglob/*"},
			wantErr: true,
		},
		{
			name:    "pkgpath-batched-load-fails",
			envs:    []golang.Environ{noGoToolEnv},
			in:      []string{"./test/goglob/..."},
			wantErr: true,
		},
		{
			name:    "pkgpath-glob-load-fails",
			envs:    []golang.Environ{noGoToolEnv},
			in:      []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/*"},
			wantErr: true,
		},
	}

	urootTestCases := []testCase{
		// GBB_PATHS, file system paths, non-Gobusybox module.
		{
			name:    "fspath-gbbpath-uroot-outside-module",
			gbbPath: []string{urootSrc},
			in: []string{
				"cmds/core/ip",
				"cmds/core/dhclient",
			},
			want: []string{
				filepath.Join(urootSrc, "cmds/core/dhclient"),
				filepath.Join(urootSrc, "cmds/core/ip"),
			},
			wantPkgPath: []string{
				"github.com/u-root/u-root/cmds/core/dhclient",
				"github.com/u-root/u-root/cmds/core/ip",
			},
		},
		// UROOT_SOURCE, file system paths, non-Gobusybox module.
		{
			name:        "fspath-uroot-source",
			urootSource: urootSrc,
			in: []string{
				"cmds/core/ip",
				"github.com/u-root/u-root/cmds/core/dhclient",
			},
			want: []string{
				filepath.Join(urootSrc, "cmds/core/dhclient"),
				filepath.Join(urootSrc, "cmds/core/ip"),
			},
			wantPkgPath: []string{
				"github.com/u-root/u-root/cmds/core/dhclient",
				"github.com/u-root/u-root/cmds/core/ip",
			},
		},
		// UROOT_SOURCE, file system paths, glob, non-Gobusybox module.
		{
			name:        "fspath-uroot-source-glob",
			urootSource: urootSrc,
			in: []string{
				"cmds/core/n*",
				"github.com/u-root/u-root/cmds/core/y*",
			},
			want: []string{
				filepath.Join(urootSrc, "cmds/core/netcat"),
				filepath.Join(urootSrc, "cmds/core/ntpdate"),
				filepath.Join(urootSrc, "cmds/core/yes"),
			},
			wantPkgPath: []string{
				"github.com/u-root/u-root/cmds/core/netcat",
				"github.com/u-root/u-root/cmds/core/ntpdate",
				"github.com/u-root/u-root/cmds/core/yes",
			},
		},
	}

	for _, tc := range append(sharedTestCases, urootTestCases...) {
		envs := []golang.Environ{moduleOffEnv, moduleOnEnv}
		if tc.envs != nil {
			envs = tc.envs
		}
		for _, env := range envs {
			t.Run(fmt.Sprintf("ResolveGlobs-GO111MODULE=%s-%s", env.GO111MODULE, tc.name), func(t *testing.T) {
				e := Env{
					GBBPath:          tc.gbbPath,
					URootSource:      tc.urootSource,
					WorkingDirectory: tc.wd,
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

	noGopathModuleOffEnv := moduleOffEnv
	noGopathModuleOffEnv.GOPATH = ""

	newPkgTests := append(sharedTestCases, testCase{
		// UROOT_SOURCE, file system paths, non-Gobusybox module.
		// Cannot resolve dependency packages without GOPATH.
		name:        "fspath-uroot-source-no-GOPATH",
		envs:        []golang.Environ{noGopathModuleOffEnv},
		urootSource: urootSrc,
		in: []string{
			"cmds/core/ip",
			"github.com/u-root/u-root/cmds/core/dhclient",
		},
		wantErr: true,
	}, testCase{
		name:    "fspath-parse-broken",
		in:      []string{"./test/parsebroken"},
		wantErr: true,
	}, testCase{
		name:    "pkgpath-parse-broken",
		in:      []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/parsebroken"},
		wantErr: true,
	})
	newPkgTests = append(newPkgTests, testCasesWithEnv([]golang.Environ{moduleOnEnv}, urootTestCases...)...)
	for _, tc := range newPkgTests {
		envs := []golang.Environ{moduleOffEnv, moduleOnEnv}
		if tc.envs != nil {
			envs = tc.envs
		}
		for _, env := range envs {
			t.Run(fmt.Sprintf("NewPackage-GO111MODULE=%s-%s", env.GO111MODULE, tc.name), func(t *testing.T) {
				e := Env{
					GBBPath:          tc.gbbPath,
					URootSource:      tc.urootSource,
					WorkingDirectory: tc.wd,
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
					pkgPaths = append(pkgPaths, p.PkgPath)
				}
				sort.Strings(pkgPaths)
				if !reflect.DeepEqual(pkgPaths, tc.wantPkgPath) {
					t.Errorf("NewPackages(%v, %v) = %v; want %v", e, tc.in, out, tc.wantPkgPath)
				}
			})

		}
	}
}

func testCasesWithEnv(envs []golang.Environ, tcs ...testCase) []testCase {
	var newTCs []testCase
	for _, tc := range tcs {
		newTC := tc
		newTC.envs = envs
		newTCs = append(newTCs, newTC)
	}
	return newTCs
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
			s:            "GBB_PATH=foo:bar UROOT_SOURCE=./foo PWD=",
			want: Env{
				GBBPath:     []string{"foo", "bar"},
				URootSource: "./foo",
			},
		},
		{
			GBB_PATH: "foo",
			s:        "GBB_PATH=foo UROOT_SOURCE= PWD=",
			want: Env{
				GBBPath: []string{"foo"},
			},
		},
		{
			s:    "GBB_PATH= UROOT_SOURCE= PWD=",
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
