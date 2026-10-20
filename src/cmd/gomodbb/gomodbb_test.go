// Copyright 2015-2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/u-root/gobusybox/src/pkg/golang"
)

func TestParsePattern(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   string
		want pattern
		// spec is what `go get` should be given, or "" when the
		// pattern is an exclusion and must not be fetched at all.
		spec string
		// cmd is what makebb should be given.
		cmd string
	}{
		{
			name: "no version query leaves the choice to go get",
			in:   "github.com/u-root/u-root/cmds/core/...",
			want: pattern{path: "github.com/u-root/u-root/cmds/core/..."},
			spec: "github.com/u-root/u-root/cmds/core/...",
			cmd:  "github.com/u-root/u-root/cmds/core/...",
		},
		{
			name: "branch query",
			in:   "github.com/u-root/u-root/cmds/core/...@main",
			want: pattern{path: "github.com/u-root/u-root/cmds/core/...", query: "main"},
			spec: "github.com/u-root/u-root/cmds/core/...@main",
			cmd:  "github.com/u-root/u-root/cmds/core/...",
		},
		{
			name: "tag query",
			in:   "github.com/u-root/u-root/cmds/core/ls@v0.16.0",
			want: pattern{path: "github.com/u-root/u-root/cmds/core/ls", query: "v0.16.0"},
			spec: "github.com/u-root/u-root/cmds/core/ls@v0.16.0",
			cmd:  "github.com/u-root/u-root/cmds/core/ls",
		},
		{
			// gopkg.in spells a major version ".vN" rather than
			// "/vN", so the path itself can end in something that
			// looks like a version. Splitting on the last "@"
			// keeps that out of the query.
			name: "path carrying a major version suffix",
			in:   "gopkg.in/yaml.v2/cmd/foo@v2.4.0",
			want: pattern{path: "gopkg.in/yaml.v2/cmd/foo", query: "v2.4.0"},
			spec: "gopkg.in/yaml.v2/cmd/foo@v2.4.0",
			cmd:  "gopkg.in/yaml.v2/cmd/foo",
		},
		{
			// A commit hash is just another query.
			name: "revision query",
			in:   "github.com/u-root/u-root/cmds/core/ls@98a84dad0e9c",
			want: pattern{path: "github.com/u-root/u-root/cmds/core/ls", query: "98a84dad0e9c"},
			spec: "github.com/u-root/u-root/cmds/core/ls@98a84dad0e9c",
			cmd:  "github.com/u-root/u-root/cmds/core/ls",
		},
		{
			// An exclusion names commands the other patterns
			// already fetched, so it is never fetched itself, but
			// makebb still has to see the leading "-".
			name: "exclusion",
			in:   "-github.com/u-root/u-root/cmds/core/ip",
			want: pattern{path: "github.com/u-root/u-root/cmds/core/ip", exclude: true},
			spec: "github.com/u-root/u-root/cmds/core/ip",
			cmd:  "-github.com/u-root/u-root/cmds/core/ip",
		},
		{
			name: "exclusion with a version query",
			in:   "-github.com/u-root/u-root/cmds/core/ip@main",
			want: pattern{path: "github.com/u-root/u-root/cmds/core/ip", query: "main", exclude: true},
			spec: "github.com/u-root/u-root/cmds/core/ip@main",
			cmd:  "-github.com/u-root/u-root/cmds/core/ip",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePattern(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parsePattern(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
			if got := got.fetchSpec(); got != tt.spec {
				t.Errorf("parsePattern(%q).fetchSpec() = %q, want %q", tt.in, got, tt.spec)
			}
			if got := parsePattern(tt.in).commandPath(); got != tt.cmd {
				t.Errorf("parsePattern(%q).commandPath() = %q, want %q", tt.in, got, tt.cmd)
			}
		})
	}
}

// The version query decides which source is fetched, and the go.mod the fetch
// writes decides it from then on. Handing makebb the query as well would be a
// second answer to a settled question, and `go list` rejects it outright:
//
//	malformed import path ".../cmds/core/ls@main": invalid char '@'
func TestCommandPathDropsTheVersionQuery(t *testing.T) {
	for _, in := range []string{
		"github.com/u-root/u-root/cmds/core/...@main",
		"-github.com/u-root/u-root/cmds/core/ip@v0.16.0",
	} {
		if got := parsePattern(in).commandPath(); strings.Contains(got, "@") {
			t.Errorf("parsePattern(%q).commandPath() = %q, want no version query", in, got)
		}
	}
}

// The go command has no "*", so a globbed pattern is widened to the "..."
// containing it, fetched, and narrowed again by matching. The point of the
// exercise is that "*" stops at a path separator where "..." does not, so
// a glob selects one level and nothing below it.
func TestGlobbedPatternSelectsOneLevel(t *testing.T) {
	for _, tt := range []struct {
		name      string
		pattern   string
		wantFetch string
		match     map[string]bool
	}{
		{
			name:      "glob is widened to the enclosing ...",
			pattern:   "m/cmds/core/*",
			wantFetch: "m/cmds/core/...",
			match: map[string]bool{
				"m/cmds/core/ls": true,
				// The case this exists for: a helper a level
				// deeper than the commands is not a command.
				"m/cmds/core/man/gen": false,
				"m/cmds/exp/cbmem":    false,
			},
		},
		{
			name:      "... is left alone and matches whatever it returned",
			pattern:   "m/cmds/core/...",
			wantFetch: "m/cmds/core/...",
			match: map[string]bool{
				"m/cmds/core/ls":      true,
				"m/cmds/core/man/gen": true,
			},
		},
		{
			name:      "a plain path is left alone",
			pattern:   "m/cmds/core/ls",
			wantFetch: "m/cmds/core/ls",
			match:     map[string]bool{"m/cmds/core/ls": true},
		},
		{
			name:      "a glob partway along widens at that element",
			pattern:   "m/cmds/*/ls",
			wantFetch: "m/cmds/...",
			match: map[string]bool{
				"m/cmds/core/ls":     true,
				"m/cmds/exp/ls":      true,
				"m/cmds/core/man/ls": false,
			},
		},
		{
			name:      "a glob within an element widens at that element",
			pattern:   "m/cmds/core/g*b",
			wantFetch: "m/cmds/core/...",
			match: map[string]bool{
				"m/cmds/core/grub": true,
				"m/cmds/core/ls":   false,
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := parsePattern(tt.pattern)
			if got := p.fetchPath(); got != tt.wantFetch {
				t.Errorf("fetchPath() = %q, want %q", got, tt.wantFetch)
			}
			for pkg, want := range tt.match {
				got, err := p.matches(pkg)
				if err != nil {
					t.Fatalf("matches(%q) error: %v", pkg, err)
				}
				if got != want {
					t.Errorf("matches(%q) = %v, want %v", pkg, got, want)
				}
			}
		})
	}
}

// A version query rides along with the widened path, not the glob, because
// the widened path is what `go get` is handed.
func TestFetchSpecWidensTheGlob(t *testing.T) {
	if got, want := parsePattern("m/cmds/core/*@main").fetchSpec(), "m/cmds/core/...@main"; got != want {
		t.Errorf("fetchSpec() = %q, want %q", got, want)
	}
	if got, want := parsePattern("m/cmds/core/*").fetchSpec(), "m/cmds/core/..."; got != want {
		t.Errorf("fetchSpec() = %q, want %q", got, want)
	}
}

func TestCheckCollisions(t *testing.T) {
	t.Run("distinct names are fine", func(t *testing.T) {
		if err := checkCollisions([]string{"m/cmds/ls", "m/cmds/ip"}); err != nil {
			t.Errorf("checkCollisions() = %v, want nil", err)
		}
	})

	t.Run("a name claimed twice is reported with both paths", func(t *testing.T) {
		err := checkCollisions([]string{"m/cmds/core/echo", "m/other/echo"})
		if err == nil {
			t.Fatal("checkCollisions() = nil, want an error")
		}
		for _, want := range []string{"echo", "m/cmds/core/echo", "m/other/echo"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("checkCollisions() = %q, want it to mention %q", err, want)
			}
		}
	})

	// Reporting only the first would make this a sequence of runs.
	t.Run("every collision is reported, not just the first", func(t *testing.T) {
		err := checkCollisions([]string{
			"m/cmds/core/echo", "m/other/echo",
			"m/cmds/core/man/gen", "m/cmds/exp/cbmem/gen",
		})
		if err == nil {
			t.Fatal("checkCollisions() = nil, want an error")
		}
		if !strings.Contains(err.Error(), "echo") || !strings.Contains(err.Error(), "gen") {
			t.Errorf("checkCollisions() = %q, want it to mention both echo and gen", err)
		}
		if !strings.Contains(err.Error(), "2 command names") {
			t.Errorf("checkCollisions() = %q, want it to count both", err)
		}
	})

	t.Run("the remedy is named", func(t *testing.T) {
		err := checkCollisions([]string{"m/a/echo", "m/b/echo"})
		if err == nil {
			t.Fatal("checkCollisions() = nil, want an error")
		}
		if !strings.Contains(err.Error(), "-m/a/echo") {
			t.Errorf("checkCollisions() = %q, want it to show an exclusion to copy", err)
		}
	})
}

func TestListCommands(t *testing.T) {
	// Stands in for `go list`, so that the set arithmetic can be checked
	// without a module on disk to ask about.
	matches := map[string][]string{
		"m/cmds/...":      {"m/cmds/ls", "m/cmds/ip", "m/cmds/wget"},
		"m/cmds/ip":       {"m/cmds/ip"},
		"m/cmds/ls":       {"m/cmds/ls"},
		"other/cmds/...":  {"other/cmds/cpud"},
		"m/cmds/notacmd":  nil,
		"m/cmds/explodes": nil,
	}
	list := func(p pattern) ([]string, error) {
		if p.path == "m/cmds/explodes" {
			return nil, errors.New("boom")
		}
		return matches[p.path], nil
	}

	for _, tt := range []struct {
		name    string
		in      []string
		want    []string
		wantErr bool
	}{
		{
			name: "one pattern keeps go list's order",
			in:   []string{"m/cmds/..."},
			want: []string{"m/cmds/ls", "m/cmds/ip", "m/cmds/wget"},
		},
		{
			name: "exclusion removes one",
			in:   []string{"m/cmds/...", "-m/cmds/ip"},
			want: []string{"m/cmds/ls", "m/cmds/wget"},
		},
		{
			// An exclusion applies to every pattern, not only the
			// one before it, so that the order of a build's
			// arguments cannot quietly change what it contains.
			name: "exclusion applies regardless of position",
			in:   []string{"-m/cmds/ip", "m/cmds/..."},
			want: []string{"m/cmds/ls", "m/cmds/wget"},
		},
		{
			// A command named by two patterns is one command.
			name: "overlapping patterns are deduplicated",
			in:   []string{"m/cmds/...", "m/cmds/ls"},
			want: []string{"m/cmds/ls", "m/cmds/ip", "m/cmds/wget"},
		},
		{
			name: "patterns across modules are concatenated",
			in:   []string{"m/cmds/...", "other/cmds/..."},
			want: []string{"m/cmds/ls", "m/cmds/ip", "m/cmds/wget", "other/cmds/cpud"},
		},
		{
			// Excluding everything is allowed; it just builds
			// nothing, which is a clearer answer than an error.
			name: "excluding everything leaves nothing",
			in:   []string{"m/cmds/ls", "-m/cmds/ls"},
			want: nil,
		},
		{
			name: "a pattern matching no command contributes none",
			in:   []string{"m/cmds/notacmd"},
			want: nil,
		},
		{
			name:    "a failing listing is reported",
			in:      []string{"m/cmds/explodes"},
			wantErr: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var patterns []pattern
			for _, s := range tt.in {
				patterns = append(patterns, parsePattern(s))
			}
			got, err := listCommands(patterns, list)
			if (err != nil) != tt.wantErr {
				t.Fatalf("listCommands() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("listCommands() = %v, want %v", got, tt.want)
			}
		})
	}
}

// lastEnv returns the value exec will use for key, which is the last one set.
func lastEnv(env []string, key string) string {
	var got string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			got = v
		}
	}
	return got
}

// gomodbb fetches through the module proxy and never through a version control
// tool, so a caller's GOPRIVATE and GONOPROXY must not survive into the go
// command: both name paths that bypass the proxy and are fetched with git.
// Leaving them in place does not merely pick a different source, it fails,
// because golang.Environ reduces PATH to $GOROOT/bin and git is not on it:
//
//	go: github.com/u-root/uio@latest: unable to resolve git version:
//	failed to execute git version: exec: "git": executable file not found in $PATH
//
// GOVCS is the setting that refuses a VCS, so it is forced even against a
// caller asking for the opposite. GOPROXY is not forced; see
// TestGoCmdKeepsTheCallersProxy.
func TestGoCmdNeverUsesAVCS(t *testing.T) {
	// Values a caller might plausibly have, each of which would otherwise
	// send the go command to a VCS. GOPROXY=direct is the most hostile of
	// them, and is left in place on purpose: GOVCS must refuse the fetch
	// without help from a pinned proxy.
	t.Setenv("GOPROXY", "direct")
	t.Setenv("GOPRIVATE", "github.com/u-root/*")
	t.Setenv("GONOPROXY", "github.com/u-root/*")
	t.Setenv("GOVCS", "*:all")

	cmd := goCmd(golang.Default(), "get", "example.com/m")

	for _, tt := range []struct {
		key  string
		want string
	}{
		{"GOPRIVATE", ""},
		{"GONOPROXY", ""},
		{"GOVCS", "*:off"},
	} {
		if got := lastEnv(cmd.Env, tt.key); got != tt.want {
			t.Errorf("goCmd() %s = %q, want %q", tt.key, got, tt.want)
		}
	}
}

// TestGoCmdKeepsTheCallersProxy pins that gomodbb never overrides GOPROXY.
//
// Using gomodbb must not require setting GOPROXY to the public proxy. It does
// not need to: the go command's own default is
// https://proxy.golang.org,direct, so a caller who has set nothing reaches the
// public proxy anyway. A caller who has set one -- a corporate mirror, or a
// cache standing in for the network so that a test can run offline -- keeps
// it, which a pin would have taken away for no gain, since GOVCS and not
// GOPROXY is what refuses a VCS.
func TestGoCmdKeepsTheCallersProxy(t *testing.T) {
	const callers = "https://proxy.example.com"
	t.Setenv("GOPROXY", callers)

	cmd := goCmd(golang.Default(), "get", "example.com/m")

	// exec takes the last value for a repeated key, so a GOPROXY appended
	// by goCmd would win over the caller's and show up here.
	if got := lastEnv(cmd.Env, "GOPROXY"); got != callers {
		t.Errorf("goCmd() GOPROXY = %q, want the caller's %q", got, callers)
	}
}

func TestModuleDir(t *testing.T) {
	t.Run("temporary directory is the caller's to remove", func(t *testing.T) {
		dir, remove, err := moduleDir("")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)
		if !remove {
			t.Error("moduleDir(\"\") said not to remove a directory it created")
		}
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("moduleDir(\"\") = %q, which does not exist: %v", dir, err)
		}
	})

	t.Run("named directory is kept and created", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "pins")
		got, remove, err := moduleDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if remove {
			t.Error("moduleDir(dir) said to remove a directory the caller named")
		}
		if !filepath.IsAbs(got) {
			t.Errorf("moduleDir(%q) = %q, want an absolute path", dir, got)
		}
		if _, err := os.Stat(got); err != nil {
			t.Errorf("moduleDir(%q) did not create it: %v", dir, err)
		}
	})
}

// -mod-dir is meant to be handed back to a later run to build the same
// versions, so fetching must not overwrite the go.mod recording them.
func TestWriteGoModKeepsAnExistingOne(t *testing.T) {
	dir := t.TempDir()
	gomod := filepath.Join(dir, "go.mod")
	existing := "module gomodbb.u-root.com/fetch\n\ngo 1.25.0\n\nrequire example.com/m v1.2.3\n"
	if err := os.WriteFile(gomod, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeGoMod(dir); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(gomod)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != existing {
		t.Errorf("writeGoMod() rewrote go.mod as %q, want it left at %q", got, existing)
	}
}

func TestWriteGoModCreatesOne(t *testing.T) {
	dir := t.TempDir()
	if err := writeGoMod(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "module " + synthModulePath + "\n"; string(got) != want {
		t.Errorf("writeGoMod() wrote %q, want %q", got, want)
	}
}
