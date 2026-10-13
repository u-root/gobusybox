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
	list := func(path string) ([]string, error) {
		if path == "m/cmds/explodes" {
			return nil, errors.New("boom")
		}
		return matches[path], nil
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

// golang.Environ sets PATH to $GOROOT/bin alone. That is invisible to a tool
// that only compiles, but the go command runs git to fetch anything the proxy
// cannot serve, and git is on the caller's PATH:
//
//	go: ...: unable to resolve git version: failed to execute git version:
//	exec: "git": executable file not found in $PATH
func TestGoCmdKeepsTheCallersPath(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")

	env := golang.Default(golang.WithGOROOT("/fake/goroot"))
	cmd := goCmd(env, "get", "example.com/m")

	// exec uses the last value for a repeated key, so the effective PATH
	// is the last one set, not the first.
	var got string
	for _, kv := range cmd.Env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			got = v
		}
	}

	want := filepath.Join("/fake/goroot", "bin") + string(os.PathListSeparator) + "/usr/bin:/bin"
	if got != want {
		t.Errorf("goCmd() PATH = %q, want %q", got, want)
	}
	if !strings.Contains(got, "/usr/bin") {
		t.Errorf("goCmd() PATH = %q, want it to keep the caller's entries", got)
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
