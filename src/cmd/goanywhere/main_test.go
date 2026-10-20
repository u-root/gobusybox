package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReturnsCommandError(t *testing.T) {
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}

	err = run(t.TempDir(), []string{goBinary, "not-a-command"}, nil)
	if err == nil {
		t.Fatal("run() returned nil for a failing command")
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run() returned %T, want *exec.ExitError", err)
	}
}

func TestWorkspaceDirCleanupRemovesTheWorkspace(t *testing.T) {
	dir, cleanup, err := newWorkspaceDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(dir), "goanywhere-") {
		t.Errorf("workspace directory %q is not named goanywhere-*", dir)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.work"), []byte("go 1.27.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cleanup()

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("workspace directory %q was not removed", dir)
	}
}

// TestCommandRemovesTheWorkspace checks that a run of the command leaves no
// workspace behind: the cleanup used to sit behind an os.Exit, so every run
// left a goanywhere-* directory in TMPDIR.
func TestCommandRemovesTheWorkspace(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "goanywhere")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	// A package of this module, so that the command has a module to write a
	// workspace for.
	pkg := filepath.Join("..", "..", "pkg", "golang")

	tmp := t.TempDir()
	cmd := exec.Command(bin, pkg, "--", "true")
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("goanywhere: %v\n%s", err, out)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "goanywhere-") {
			t.Errorf("goanywhere left the workspace %q behind", filepath.Join(tmp, entry.Name()))
		}
	}
}
