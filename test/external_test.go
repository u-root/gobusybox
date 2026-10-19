// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// external gates the tests in this file, which fetch from the module proxy
// and take minutes. They are off by default so that `go test ./...` in a
// checkout stays fast and offline.
var external = flag.Bool("external", false, "run tests that fetch external modules")

// The external modules, pinned to a revision rather than tracked.
//
// A branch name would make these tests fail on the day someone else's default
// branch moves, which is a failure belonging to nobody here and arrives
// without a commit of ours to blame.
const (
	// urootRev is used twice: once for the fetch below, and once for the
	// module path gomodbb fetches. Keeping it in one constant stops the
	// two from drifting apart and testing different trees.
	urootRev   = "6ca118b0a77c23ae859cddeee15762d9cd74c63f"
	gokrazyRev = "254af2bf3c82ff9f56e89794b2c146ef9cc85dc6"
	p9Rev      = "660eb2337e3c1878298fe550ad03248f329eeb72"

	urootMod   = "github.com/u-root/u-root"
	gokrazyMod = "github.com/gokrazy/gokrazy"
	p9Mod      = "github.com/hugelgupf/p9"
)

// makeWritable restores write permission throughout dir.
//
// The go tool marks everything it writes into a module cache read-only, so a
// plain RemoveAll fails partway with "permission denied" (golang/go#27455).
// The shell script this replaced worked around that with `go clean -modcache`
// -- but it wrote `go clean -cache -modcache`, and the -cache half ignores
// GOPATH and empties GOCACHE instead, which is the developer's real build
// cache rather than anything this test created.
//
// Restoring write permission removes the need to run `go clean` at all, so
// nothing outside the directory passed here is touched.
func makeWritable(dir string) {
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		mode := fs.FileMode(0o600)
		if d.IsDir() {
			mode = 0o700
		}
		os.Chmod(path, mode)
		return nil
	})
}

// scratchGOPATH returns an empty GOPATH, removed when the test ends.
//
// It is deliberately empty rather than the caller's: building against a cold
// module cache is the property under test, since it proves the tools fetch
// what they need instead of relying on something a previous run left behind.
//
// t.TempDir cannot remove a module cache by itself -- its cleanup is a plain
// RemoveAll, and it reports the failure against the test that used it:
//
//	testing.go: TempDir RemoveAll cleanup: unlinkat .../vecnet_test.go:
//	permission denied
//
// Cleanups run in last-added-first-called order, though, and t.TempDir
// registers its removal when it is first called. Restoring write permission
// in a cleanup added after that line therefore happens before the removal,
// which is all a module cache needs.
func scratchGOPATH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(func() { makeWritable(dir) })
	return dir
}

// run executes name with args in dir, and fails the test if it does not
// succeed. The environment is inherited, so GOCOVERDIR reaches the tools
// built by buildTool and coverage is collected without any extra plumbing.
func run(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	t.Logf("Run: %s %s", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("%s output:\n%s", filepath.Base(name), out)
		t.Fatalf("%s failed: %v", filepath.Base(name), err)
	}
	return string(out)
}

// buildTool returns the binary to test for one of this repository's commands,
// compiling it into a directory of the calling test's own.
//
// Building on demand is what lets `cd test && go test ./...` work on its own.
// The shell script this replaced existed largely to compile makebb and pass
// its path in, which meant these tests could not be run the way every other
// Go test in the tree is run.
//
// Each call gets its own t.TempDir rather than sharing one for the whole run.
// A shared directory would have to outlive every test, which means creating
// it in TestMain, where there is no *testing.T to hang its removal on. A
// warm rebuild costs about half a second against a suite that takes minutes,
// so caching the binaries bought a package-level directory, a mutex and a map
// for no time worth having.
//
// -covermode=atomic makes the binary write a coverage profile to GOCOVERDIR
// when it runs, which is how CI reports coverage for code that is only ever
// reached through a subprocess.
//
// -buildvcs=false because src/cmd/* sits inside this repository's checkout,
// so go build would otherwise run git to stamp the commit into the binary.
// That is the last thing in the suite that reached for a version control
// tool, and the stamp is of no use to a binary built to be tested. Leaving it
// to the default is not enough: `auto` skips the stamp when git is missing,
// but still fails the build when a git that is present cannot answer.
func buildTool(t *testing.T, name string) string {
	t.Helper()

	// An explicitly supplied makebb wins, so CI can test a binary built
	// some other way.
	if name == "makebb" && *makebb != "" {
		p, err := filepath.Abs(*makebb)
		if err != nil {
			t.Fatalf("Could not resolve -makebb: %v", err)
		}
		return p
	}

	src, err := filepath.Abs(filepath.Join("..", "src", "cmd", name))
	if err != nil {
		t.Fatalf("Could not resolve %s: %v", name, err)
	}
	out := filepath.Join(t.TempDir(), name)

	t.Logf("Build: go build -covermode=atomic -buildvcs=false -o %s ./src/cmd/%s", out, name)
	cmd := exec.Command("go", "build", "-covermode=atomic", "-buildvcs=false", "-o", out, ".")
	cmd.Dir = src
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Could not build %s: %v\n%s", name, err, b)
	}

	return out
}

// fetchModule unpacks one module at one revision into dir/name.
//
// The source comes from the module proxy, never from a version control tool.
// The go command resolves a bare commit hash to a pseudo-version through the
// proxy, so a hash still pins the tree exactly as `git checkout` did -- the
// reported Origin names the same commit.
//
// Which proxy is the caller's to choose, so this sets no GOPROXY at all. An
// internal mirror has to keep working, `go env -w` records one as readily as
// the environment does, and Go's own default is the public proxy already, so
// there is nothing here worth overriding a developer's configuration for.
//
// GOVCS=*:off is the guarantee, not the proxy. Whatever GOPROXY turns out to
// be -- including the `direct` half of Go's default, which exists precisely
// to fall back to a VCS -- a fetch that reaches for one is refused with a
// policy error rather than quietly shelling out to git.
//
// GOPRIVATE and GONOPROXY are cleared, because they do not name a proxy to
// use: they name paths that skip whichever proxy is configured and go
// straight to a VCS. These three modules are public, so a caller's rule
// matching them would become a GOVCS refusal and fail the suite over a
// setting that has nothing to do with it.
//
// Fetching with `git clone` would have left the suite needing a tool that is
// no more portable than the shell it replaced, which is most of what issue
// #22 was about. Nothing here requires git to exist.
//
// The proxy serves the module zip, which differs from a checkout in ways that
// do not reach this test: it carries no VCS metadata, no nested modules and
// no symlinks, and it omits a module's own vendor directory. u-root vendors
// its dependencies, so that last one applies -- but the workspace build
// ignores a member module's vendor directory anyway, and the `vendor` subtest
// below builds its own with `go work vendor`.
func fetchModule(t *testing.T, dir, name, modPath, rev string, env []string) {
	t.Helper()

	// Resolve and download in a directory of its own. `go mod download`
	// takes an explicit module@version outside any module, and running it
	// somewhere empty keeps it that way: dir acquires a go.work later, and
	// a module context here would change what gets resolved.
	cmd := exec.Command("go", "mod", "download", "-json", modPath+"@"+rev)
	cmd.Dir = t.TempDir()
	cmd.Env = append(append(os.Environ(), env...),
		"GOPRIVATE=",
		"GONOPROXY=",
		"GOVCS=*:off",
	)
	// Separate pipes, because the JSON is on stdout and `go: downloading`
	// progress is on stderr; combining them is not parseable.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	t.Logf("Run: go mod download -json %s@%s", modPath, rev)
	runErr := cmd.Run()

	var mod struct {
		Version string
		Dir     string
		Error   string
	}
	// Parse before reporting a non-zero exit. A download that fails is
	// reported in the JSON's Error field with nothing on stderr at all, so
	// acting on the exit status first would throw the reason away and
	// leave "exit status 1" as the whole explanation. A failure with no
	// JSON at all is a different thing -- the go command could not get far
	// enough to have an answer -- and for that stderr is all there is.
	if err := json.Unmarshal(stdout.Bytes(), &mod); err != nil {
		if runErr != nil {
			t.Fatalf("Could not download %s@%s: %v\n%s", modPath, rev, runErr, stderr.String())
		}
		t.Fatalf("Could not parse `go mod download -json %s@%s`: %v\n%s", modPath, rev, err, stdout.String())
	}
	if mod.Error != "" {
		t.Fatalf("Could not download %s@%s: %s", modPath, rev, mod.Error)
	}
	if runErr != nil {
		t.Fatalf("Could not download %s@%s: %v\n%s", modPath, rev, runErr, stderr.String())
	}
	if mod.Dir == "" {
		t.Fatalf("`go mod download -json %s@%s` named no directory:\n%s", modPath, rev, stdout.String())
	}
	t.Logf("Fetched %s@%s as %s", modPath, rev, mod.Version)

	// The build writes into these trees, and everything the go command
	// puts in a module cache is read-only. CopyFS creates files writable
	// whatever the source mode was, so the copy needs no chmod pass and
	// t.TempDir can remove it unaided. It rejects symlinks, which a module
	// zip never contains, so an unexpected one fails loudly here.
	if err := os.CopyFS(filepath.Join(dir, name), os.DirFS(mod.Dir)); err != nil {
		t.Fatalf("Could not copy %s to %s: %v", mod.Dir, name, err)
	}
}

// commandsIn expands the shell globs the script used to name the commands
// under test, e.g. ./u-root/cmds/*/* . It fails rather than returning an
// empty list, because makebb given no commands is not the test anybody meant
// to run and would otherwise pass quietly.
func commandsIn(t *testing.T, dir, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		t.Fatalf("Bad pattern %s: %v", pattern, err)
	}
	var cmds []string
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil || !fi.IsDir() {
			continue
		}
		rel, err := filepath.Rel(dir, m)
		if err != nil {
			t.Fatalf("Could not relativize %s: %v", m, err)
		}
		cmds = append(cmds, "./"+filepath.ToSlash(rel))
	}
	if len(cmds) == 0 {
		t.Fatalf("Pattern %s matched no command directories", pattern)
	}
	return cmds
}

// TestExternalWorkspaces builds real third-party commands -- u-root, gokrazy
// and p9 -- into a busybox, through a Go workspace spanning three modules.
//
// This is the test that catches breakage the fixtures under test/ cannot,
// because those are small and this is not.
func TestExternalWorkspaces(t *testing.T) {
	if !*external {
		t.Skip("Skipping; pass -external to fetch external modules and run this")
	}

	dir := t.TempDir()
	gopath := scratchGOPATH(t)
	env := []string{"GOPATH=" + gopath, "GO111MODULE=on"}

	makebbBin := buildTool(t, "makebb")
	goanywhereBin := buildTool(t, "goanywhere")

	fetchModule(t, dir, "u-root", urootMod, urootRev, env)
	fetchModule(t, dir, "gokrazy", gokrazyMod, gokrazyRev, env)
	fetchModule(t, dir, "p9", p9Mod, p9Rev, env)

	// A workspace spanning all three modules.
	run(t, dir, env, "go", "work", "init", "./u-root")
	run(t, dir, env, "go", "work", "use", "./gokrazy")
	run(t, dir, env, "go", "work", "use", "./p9")

	urootCmds := commandsIn(t, dir, "u-root/cmds/*/*")
	gokrazyCmds := commandsIn(t, dir, "gokrazy/cmd/*")
	p9Cmds := commandsIn(t, dir, "p9/cmd/*")

	allCmds := append(append(append([]string{}, urootCmds...), gokrazyCmds...), p9Cmds...)

	t.Run("reproducible", func(t *testing.T) {
		// Two builds of the same commands must be byte-identical.
		// Reproducibility is a headline property of a busybox, and it
		// is easy to lose by accident: an absolute path or a map
		// iteration order reaching the generated source is enough.
		for _, out := range []string{"bb1", "bb2"} {
			args := append([]string{"-go-mod=readonly", "-o", out}, urootCmds...)
			run(t, dir, env, makebbBin, args...)
		}

		bb1, err := os.ReadFile(filepath.Join(dir, "bb1"))
		if err != nil {
			t.Fatalf("Could not read bb1: %v", err)
		}
		bb2, err := os.ReadFile(filepath.Join(dir, "bb2"))
		if err != nil {
			t.Fatalf("Could not read bb2: %v", err)
		}
		if !bytes.Equal(bb1, bb2) {
			t.Errorf("Building u-root twice gave different binaries (%d and %d bytes); builds are not reproducible", len(bb1), len(bb2))
		}
		for _, f := range []string{"bb1", "bb2"} {
			os.Remove(filepath.Join(dir, f))
		}
	})

	// Cross-compilation, across all three modules at once. The busybox is
	// not run, because the host cannot execute two of these.
	for _, goarch := range []string{"amd64", "arm64", "riscv64"} {
		t.Run("GOARCH="+goarch, func(t *testing.T) {
			args := append([]string{"-go-mod=readonly", "-o", "bb-" + goarch}, allCmds...)
			run(t, dir, append(env, "GOARCH="+goarch), makebbBin, args...)
			if _, err := os.Stat(filepath.Join(dir, "bb-"+goarch)); err != nil {
				t.Fatalf("No busybox for %s: %v", goarch, err)
			}
		})
	}

	t.Run("vendor", func(t *testing.T) {
		// An offline build, from a vendor directory rather than the
		// module cache. `go work vendor` needs Go 1.22; src/go.mod
		// requires 1.27, so there is no version to skip for.
		run(t, dir, env, "go", "work", "vendor")
		t.Cleanup(func() { os.RemoveAll(filepath.Join(dir, "vendor")) })

		args := append([]string{"-go-mod=vendor", "-o", "bb-vendor"}, allCmds...)
		run(t, dir, append(env, "GOARCH=amd64"), makebbBin, args...)
	})

	t.Run("goanywhere", func(t *testing.T) {
		// goanywhere builds commands that are not in one workspace by
		// generating one, so remove the workspace first: leaving it
		// would mean this passes whether or not goanywhere works.
		for _, f := range []string{"go.work", "go.work.sum"} {
			if err := os.Remove(filepath.Join(dir, f)); err != nil && !os.IsNotExist(err) {
				t.Fatalf("Could not remove %s: %v", f, err)
			}
		}

		// -o names a directory rather than a file, as the shell script
		// did with `-o $(pwd)`. makebb treats that as "put bb in
		// here", and nothing else in the suite asks for it, so passing
		// a file path here would quietly drop the only coverage that
		// branch has.
		out := t.TempDir()
		args := append([]string{"-d"}, append(append([]string{}, urootCmds...), p9Cmds...)...)
		args = append(args, "--", makebbBin, "-o", out)
		run(t, dir, env, goanywhereBin, args...)

		if _, err := os.Stat(filepath.Join(out, "bb")); err != nil {
			t.Fatalf("goanywhere produced no busybox at %s: %v", filepath.Join(out, "bb"), err)
		}
	})
}

// TestGomodbb builds a busybox from the module proxy, with no source tree.
//
// gomodbb fetches its own source, so unlike everything above it needs no
// unpacked module and no workspace. It runs in an empty directory with no
// module and no go.work anywhere above it, so that a regression where it
// quietly starts relying on one cannot pass unnoticed.
func TestGomodbb(t *testing.T) {
	if !*external {
		t.Skip("Skipping; pass -external to fetch from the module proxy and run this")
	}

	dir := t.TempDir()
	gopath := scratchGOPATH(t)
	env := []string{"GOPATH=" + gopath}

	gomodbbBin := buildTool(t, "gomodbb")

	// The same revision the workspace test uses. The same source reached
	// two ways -- an unpacked module and gomodbb's own fetch -- should
	// build the same commands, and pinning both to one constant asks that.
	const corePattern = "github.com/u-root/u-root/cmds/core/...@" + urootRev

	t.Run("list", func(t *testing.T) {
		out := run(t, dir, env, gomodbbBin, "-l", corePattern)
		if !strings.Contains(out, "github.com/u-root/u-root/cmds/core/ls\n") {
			t.Errorf("gomodbb -l did not list ls; got:\n%s", out)
		}
	})

	t.Run("build", func(t *testing.T) {
		run(t, dir, env, gomodbbBin, "-o", "bb-gomodbb",
			"github.com/u-root/u-root/cmds/core/dmesg@"+urootRev,
			"github.com/u-root/u-root/cmds/core/ls@"+urootRev)

		// Dispatch by argv[1]: argv[0] here is the busybox's own name,
		// which is not a command in it.
		run(t, dir, env, filepath.Join(dir, "bb-gomodbb"), "ls")
	})
}
