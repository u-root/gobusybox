// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// mkfile creates a regular file, and every directory leading to it.
func mkfile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mklink creates a symlink at link pointing at target, which may be relative.
func mklink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// TestResolveUntilLastSymlink pins the behaviour the busybox depends on: the
// result is the last *symlink* in a chain, not the file it points at. A
// busybox dispatches on the base name of os.Args[0], so resolving one hop too
// far would name the busybox itself rather than the command the caller asked
// for.
func TestResolveUntilLastSymlink(t *testing.T) {
	for _, tt := range []struct {
		name string
		// build lays out a tree under dir and returns the path to
		// resolve and the path wanted back, both absolute.
		build func(t *testing.T, dir string) (in, want string)
	}{
		{
			// /bbin/bb itself: a regular file is already the answer.
			name: "regular file",
			build: func(t *testing.T, d string) (string, string) {
				mkfile(t, d+"/bb")
				return d + "/bb", d + "/bb"
			},
		},
		{
			name: "nonexistent path",
			build: func(t *testing.T, d string) (string, string) {
				return d + "/nope", d + "/nope"
			},
		},
		{
			// The ordinary busybox layout: /bbin/ls -> bb. The
			// symlink is the answer, not bb.
			name: "symlink to regular file",
			build: func(t *testing.T, d string) (string, string) {
				mkfile(t, d+"/bb")
				mklink(t, d+"/bb", d+"/ls")
				return d + "/ls", d + "/ls"
			},
		},
		{
			// The example in ResolveUntilLastSymlink's own comment.
			name: "relative chain stops at last symlink",
			build: func(t *testing.T, d string) (string, string) {
				mkfile(t, d+"/baz/bla")
				mklink(t, "bla", d+"/baz/foo")
				mklink(t, "../baz/foo", d+"/foo/bar")
				return d + "/foo/bar", d + "/baz/foo"
			},
		},
		{
			// /bin/defaultsh -> ../bbin/elvish, from AbsSymlink's
			// comment: a relative target resolves against the
			// link's own directory, not the working directory.
			name: "relative target resolves against link directory",
			build: func(t *testing.T, d string) (string, string) {
				mkfile(t, d+"/bbin/bb")
				mklink(t, "bb", d+"/bbin/elvish")
				mklink(t, "../bbin/elvish", d+"/bin/defaultsh")
				return d + "/bin/defaultsh", d + "/bbin/elvish"
			},
		},
		{
			name: "long absolute chain",
			build: func(t *testing.T, d string) (string, string) {
				mkfile(t, d+"/bb")
				mklink(t, d+"/bb", d+"/l0")
				mklink(t, d+"/l0", d+"/l1")
				mklink(t, d+"/l1", d+"/l2")
				return d + "/l2", d + "/l0"
			},
		},
		{
			// A broken link is still the last symlink, so it is
			// still the answer.
			name: "dangling symlink",
			build: func(t *testing.T, d string) (string, string) {
				mklink(t, d+"/missing", d+"/dangle")
				return d + "/dangle", d + "/dangle"
			},
		},
		{
			name: "chain ending in a dangling symlink",
			build: func(t *testing.T, d string) (string, string) {
				mklink(t, d+"/missing", d+"/b")
				mklink(t, d+"/b", d+"/a")
				return d + "/a", d + "/b"
			},
		},
		{
			name: "symlink to a directory",
			build: func(t *testing.T, d string) (string, string) {
				if err := os.MkdirAll(d+"/realdir", 0o755); err != nil {
					t.Fatal(err)
				}
				mklink(t, d+"/realdir", d+"/dirlink")
				return d + "/dirlink", d + "/dirlink"
			},
		},
		{
			// Only the final component is walked. A symlinked
			// directory in the path is resolved by the kernel, and
			// must not be mistaken for a hop.
			name: "symlink in a directory component",
			build: func(t *testing.T, d string) (string, string) {
				mkfile(t, d+"/real/bb")
				mklink(t, d+"/real", d+"/alias")
				mklink(t, "bb", d+"/real/sh")
				return d + "/alias/sh", d + "/alias/sh"
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in, want := tt.build(t, t.TempDir())
			if got := ResolveUntilLastSymlink(in); got != want {
				t.Errorf("ResolveUntilLastSymlink(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

// TestResolveUntilLastSymlinkRelativeArg covers a relative os.Args[0], which
// sends AbsSymlink down its filepath.Abs branch.
func TestResolveUntilLastSymlinkRelativeArg(t *testing.T) {
	d := t.TempDir()
	mkfile(t, d+"/bb")
	mklink(t, "bb", d+"/a")
	mklink(t, "a", d+"/b")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Error(err)
		}
	})
	if err := os.Chdir(d); err != nil {
		t.Fatal(err)
	}

	// t.TempDir can sit under a symlinked path -- /tmp is a symlink on
	// macOS -- so compare against the resolved directory rather than d.
	realDir, err := filepath.EvalSymlinks(d)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(realDir, "a")
	if got := ResolveUntilLastSymlink("b"); got != want {
		t.Errorf("ResolveUntilLastSymlink(%q) = %q, want %q", "b", got, want)
	}
}
