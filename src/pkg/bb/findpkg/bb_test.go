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

func TestResolveGlobsGobusyboxGOPATH(t *testing.T) {
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

	if err := os.Mkdir("./test/broken", 0777); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll("./test/broken")
	if err := ioutil.WriteFile("./test/broken/main.go", []byte("broken"), 0777); err != nil {
		t.Fatal(err)
	}

	l := &ulogtest.Logger{TB: t}

	for _, tc := range []struct {
		name        string
		envs        []golang.Environ
		wd          string
		urootSource string
		gbbPath     []string
		in          []string
		want        []string
		wantErr     bool
		err         error
	}{
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
			want: []string{filepath.Join(gbbmod, "cmd/makebb")},
		},
		// Single package, file system path, GBB_PATHS.
		{
			name:    "fspath-gbbpath-single",
			gbbPath: []string{gbbmod},
			in:      []string{"cmd/makebb"},
			want:    []string{filepath.Join(gbbmod, "cmd/makebb")},
		},
		// Single package, Go package path.
		{
			name: "pkgpath-single",
			in:   []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
			want: []string{"github.com/u-root/gobusybox/src/cmd/makebb"},
		},
		// Two packages, globbed file system path.
		{
			name: "fspath-glob",
			in:   []string{filepath.Join(gbbmod, "cmd/make*")},
			want: []string{filepath.Join(gbbmod, "cmd/makebb"), filepath.Join(gbbmod, "cmd/makebbmain")},
		},
		// Two packages, globbed Go package path.
		{
			name: "pkgpath-glob",
			in:   []string{"github.com/u-root/gobusybox/src/cmd/make*"},
			want: []string{"github.com/u-root/gobusybox/src/cmd/makebb", "github.com/u-root/gobusybox/src/cmd/makebbmain"},
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
			want: []string{filepath.Join(gbbmod, "cmd/makebb")},
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
		{
			name:    "fspath-broken-go",
			in:      []string{"./test/broken"},
			wantErr: true,
		},
		{
			name:    "pkgpath-broken-go",
			in:      []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/broken"},
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
		},
		// Exclusion, single package, file system path.
		{
			name: "fspath-exclusion",
			in:   []string{"./test/goglob/*", "-test/goglob/echo"},
			want: []string{filepath.Join(gbbmod, "pkg/bb/findpkg/test/goglob/foo")},
		},
		// Exclusion, single package, Go package path.
		{
			name: "pkgpath-exclusion",
			in:   []string{"./test/goglob/...", "-github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/echo"},
			want: []string{"github.com/u-root/gobusybox/src/pkg/bb/findpkg/test/goglob/foo"},
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
		},
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
		},
	} {
		envs := []golang.Environ{moduleOffEnv, moduleOnEnv}
		if tc.envs != nil {
			envs = tc.envs
		}
		for _, env := range envs {
			t.Run(fmt.Sprintf("GO111MODULE=%s-%s", env.GO111MODULE, tc.name), func(t *testing.T) {
				e := Env{GBBPath: tc.gbbPath, URootSource: tc.urootSource, WorkingDirectory: tc.wd}
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
}
