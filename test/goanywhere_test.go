// Copyright 2026 the u-root Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGoanywhereRemovesItsTempWorkspace pins the fix for the workspace
// goanywhere leaked on every run: main exited through os.Exit, so the
// deferred cleanup never ran.
func TestGoanywhereRemovesItsTempWorkspace(t *testing.T) {
	goanywhere := buildTool(t, "goanywhere")
	makebbBin := buildTool(t, "makebb")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	tmp := t.TempDir()
	// os.TempDir reads TMPDIR on Unix and TMP/TEMP on Windows; set all three
	// so the test checks the workspace the tool actually creates on any host.
	env := []string{"TMPDIR=" + tmp, "TMP=" + tmp, "TEMP=" + tmp}

	run(t, t.TempDir(), env, goanywhere, filepath.Join(wd, "globalvar"), "--", makebbBin, "-o", out)

	if _, err := os.Stat(filepath.Join(out, "bb")); err != nil {
		t.Fatalf("goanywhere produced no busybox at %s: %v", filepath.Join(out, "bb"), err)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("goanywhere left %d entries in its temp dir: %v", len(entries), names)
	}
}
