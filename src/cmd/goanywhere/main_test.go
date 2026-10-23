// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	// Successful command should return nil
	if err := run(tmpDir, []string{"go", "version"}, nil); err != nil {
		t.Fatalf("run(go version) = %v, want nil", err)
	}

	// Failing command should return non-nil error without calling os.Exit
	if err := run(tmpDir, []string{"go", "non-existent-command-xyz"}, nil); err == nil {
		t.Fatalf("run(failing command) = nil, want error")
	}
}

func TestCreateWorkspaceAndRunTmpCleanup(t *testing.T) {
	tmpParent := t.TempDir()
	t.Setenv("TMPDIR", tmpParent)
	t.Setenv("TMP", tmpParent)
	t.Setenv("TEMP", tmpParent)

	mods := map[string][]string{
		".": {"."},
	}

	err := createWorkspaceAndRun("", true, "1.27.0", mods, []string{"go", "version"}, nil)
	if err != nil {
		t.Fatalf("createWorkspaceAndRun failed: %v", err)
	}

	entries, err := os.ReadDir(tmpParent)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "goanywhere-") {
			t.Errorf("Temporary directory %s was leaked and not removed", filepath.Join(tmpParent, e.Name()))
		}
	}
}

func TestCreateWorkspaceAndRunTmpCleanupOnFailure(t *testing.T) {
	tmpParent := t.TempDir()
	t.Setenv("TMPDIR", tmpParent)
	t.Setenv("TMP", tmpParent)
	t.Setenv("TEMP", tmpParent)

	mods := map[string][]string{
		".": {"."},
	}

	// Failing command
	err := createWorkspaceAndRun("", true, "1.27.0", mods, []string{"go", "non-existent-command-xyz"}, nil)
	if err == nil {
		t.Fatalf("createWorkspaceAndRun with invalid command succeeded, want error")
	}

	entries, err := os.ReadDir(tmpParent)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "goanywhere-") {
			t.Errorf("Temporary directory %s was leaked on command failure", filepath.Join(tmpParent, e.Name()))
		}
	}
}

func TestCreateWorkspaceAndRunNoTmpCleanup(t *testing.T) {
	workDir := t.TempDir()
	mods := map[string][]string{
		workDir: {workDir},
	}

	err := createWorkspaceAndRun(workDir, false, "1.27.0", mods, []string{"go", "version"}, nil)
	if err != nil {
		t.Fatalf("createWorkspaceAndRun failed: %v", err)
	}

	goWorkPath := filepath.Join(workDir, "go.work")
	if _, err := os.Stat(goWorkPath); !os.IsNotExist(err) {
		t.Errorf("go.work at %s was not removed after -tmp=false run", goWorkPath)
	}
}

func TestCreateWorkspaceAndRunNoTmpCleanupOnFailure(t *testing.T) {
	workDir := t.TempDir()
	mods := map[string][]string{
		workDir: {workDir},
	}

	err := createWorkspaceAndRun(workDir, false, "1.27.0", mods, []string{"go", "non-existent-command-xyz"}, nil)
	if err == nil {
		t.Fatalf("createWorkspaceAndRun with invalid command succeeded, want error")
	}

	goWorkPath := filepath.Join(workDir, "go.work")
	if _, err := os.Stat(goWorkPath); !os.IsNotExist(err) {
		t.Errorf("go.work at %s was not removed after failing -tmp=false run", goWorkPath)
	}
}
