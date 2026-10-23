// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestGoanywhereCleanup(t *testing.T) {
	workDir := t.TempDir()
	goMod := "module example.com/test\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	origArgs := os.Args
	origCommandLine := flag.CommandLine
	defer func() {
		os.Args = origArgs
		flag.CommandLine = origCommandLine
	}()

	// Test 1: Successful command with -tmp=true
	flag.CommandLine = flag.NewFlagSet("goanywhere", flag.ContinueOnError)
	debug = flag.CommandLine.Bool("d", false, "Show debug string")
	tmp = flag.CommandLine.Bool("tmp", true, "Create workspace in temp dir")
	versionFlag = flag.CommandLine.String("v", "", "Go version for workspace file")
	os.Args = []string{"goanywhere", workDir, "--", "go", "version"}

	if err := run(); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	// Test 2: Failing command with -tmp=false removes generated go.work
	flag.CommandLine = flag.NewFlagSet("goanywhere", flag.ContinueOnError)
	debug = flag.CommandLine.Bool("d", false, "Show debug string")
	tmp = flag.CommandLine.Bool("tmp", false, "Create workspace in temp dir")
	versionFlag = flag.CommandLine.String("v", "", "Go version for workspace file")
	os.Args = []string{"goanywhere", "-tmp=false", workDir, "--", "go", "non-existent-subcommand-xyz"}

	err := run()
	if err == nil {
		t.Fatalf("run with failing command succeeded, want error")
	}

	goWorkPath := filepath.Join(workDir, "go.work")
	if _, statErr := os.Stat(goWorkPath); !os.IsNotExist(statErr) {
		t.Errorf("go.work at %s was not cleaned up on failure", goWorkPath)
	}
}
