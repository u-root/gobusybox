// Copyright 2015-2024 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package golang is an API to the Go compiler.
package golang

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestUrootUsage makes sure that no changes break u-root.
func TestUrootUsage(t *testing.T) {
	_ = Default().GoCmd("tool", "doc", "fmt")
}

func TestVersionLine(t *testing.T) {
	for _, tt := range []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{
			name: "plain",
			in:   "go version go1.22.4 linux/amd64\n",
			want: "go version go1.22.4 linux/amd64",
		},
		{
			name: "no trailing newline",
			in:   "go version go1.22.4 linux/amd64",
			want: "go version go1.22.4 linux/amd64",
		},
		// The regression this parser exists for: GOTOOLCHAIN selects a
		// toolchain that is not installed, so the fetch is reported on
		// the same stream before the version itself. Taking the first
		// field of the whole output yielded "go:", and every build
		// failed with "go-compiler 'version' output unrecognized".
		{
			name: "leading toolchain download",
			in:   "go: downloading go1.22.4 (linux/amd64)\ngo version go1.22.4 linux/amd64\n",
			want: "go version go1.22.4 linux/amd64",
		},
		{
			name: "several leading progress lines",
			in: "go: downloading go1.22.4 (linux/amd64)\n" +
				"go: download go1.22.4 for linux/amd64\n" +
				"go version go1.22.4 linux/amd64\n",
			want: "go version go1.22.4 linux/amd64",
		},
		{
			name: "trailing blank lines",
			in:   "go version go1.22.4 linux/amd64\n\n\n",
			want: "go version go1.22.4 linux/amd64",
		},
		{
			name: "trailing whitespace-only lines",
			in:   "go version go1.22.4 linux/amd64\n   \n\t\n",
			want: "go version go1.22.4 linux/amd64",
		},
		{
			name: "download and trailing blanks together",
			in:   "go: downloading go1.22.4 (linux/amd64)\ngo version go1.22.4 linux/amd64\n\n",
			want: "go version go1.22.4 linux/amd64",
		},
		// Carriage returns must not survive into the version line, or
		// the final field parses as "amd64\r".
		{
			name: "crlf",
			in:   "go: downloading go1.22.4 (linux/amd64)\r\ngo version go1.22.4 linux/amd64\r\n",
			want: "go version go1.22.4 linux/amd64",
		},
		// CompilerInit parses tinygo's longer line out of the same
		// helper, so it must pass through untouched.
		{
			name: "tinygo",
			in:   "tinygo version 0.33.0 darwin/arm64 (using go version go1.22.2 and LLVM version 18.1.2)\n",
			want: "tinygo version 0.33.0 darwin/arm64 (using go version go1.22.2 and LLVM version 18.1.2)",
		},
		// No version line at all is reported as ErrNoVersionString
		// rather than an empty string, so CompilerInit never reaches s[0].
		{
			name:    "empty",
			in:      "",
			want:    "",
			wantErr: ErrNoVersionString,
		},
		{
			name:    "whitespace only",
			in:      "\n  \n\t\n",
			want:    "",
			wantErr: ErrNoVersionString,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := versionLine(tt.in)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("versionLine(%q) error = %v, want %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("versionLine(%q) = %q, want %q", tt.in, got, tt.want)
			}
			// Asserted for every case, not just the two that expect an
			// error: the table alone would let a later entry declare
			// ("", nil) to be correct. CompilerInit indexes s[0]
			// without a length check, so that pairing panics.
			if err == nil && got == "" {
				t.Errorf("versionLine(%q) = (%q, nil), want a non-empty line or an error", tt.in, got)
			}
		})
	}
}

// FuzzVersionLine holds the same invariants as TestVersionLine over arbitrary
// input, where a table cannot: versionLine must never report success with an
// empty line, must use ErrNoVersionString for its only failure, and must
// return a
// line with no surrounding whitespace.
func FuzzVersionLine(f *testing.F) {
	for _, seed := range []string{
		"",
		"\n",
		"  \t\n",
		"\r\n\r\n",
		"go version go1.22.4 linux/amd64\n",
		"go: downloading go1.22.4 (linux/amd64)\ngo version go1.22.4 linux/amd64\n",
		"tinygo version 0.33.0 darwin/arm64 (using go version go1.22.2 and LLVM version 18.1.2)\n",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, out string) {
		got, err := versionLine(out)
		if err == nil && got == "" {
			t.Errorf("versionLine(%q) = (%q, nil), want a non-empty line or an error", out, got)
		}
		if err != nil && !errors.Is(err, ErrNoVersionString) {
			t.Errorf("versionLine(%q) error = %v, want ErrNoVersionString", out, err)
		}
		if err != nil && got != "" {
			t.Errorf("versionLine(%q) = (%q, %v), want an empty line on error", out, got, err)
		}
		if got != strings.TrimSpace(got) {
			t.Errorf("versionLine(%q) = %q, want no surrounding whitespace", out, got)
		}
	})
}

// TestCompilerInitErrors checks that CompilerInit propagates versionLine's
// error rather than swallowing it, and reports a version line it cannot parse
// distinguishably from output that held no version line at all.
func TestCompilerInitErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake compiler is a shell script")
	}

	for _, tt := range []struct {
		name    string
		output  string // what the fake compiler prints for `version`
		wantErr error
	}{
		// No version line: versionLine's error must reach the caller.
		{
			name:    "no output",
			output:  "",
			wantErr: ErrNoVersionString,
		},
		{
			name:    "blank lines only",
			output:  "\n  \n\t\n",
			wantErr: ErrNoVersionString,
		},
		// A line exists but does not parse. Distinct from the above.
		{
			name:    "go line too short",
			output:  "go version\n",
			wantErr: ErrVersionSyntax,
		},
		{
			name:    "unknown compiler",
			output:  "rustc 1.77.0 (aedd173a2 2024-03-17)\n",
			wantErr: ErrVersionSyntax,
		},
		// Stops before tinygo's `info -json` call, which the fake does
		// not implement.
		{
			name:    "tinygo line too short",
			output:  "tinygo version 0.33.0\n",
			wantErr: ErrVersionSyntax,
		},
		// The regression this all exists for still has to succeed.
		{
			name:   "download noise then a good line",
			output: "go: downloading go1.22.4 (linux/amd64)\ngo version go1.22.4 linux/amd64\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fakego")
			// Environ.Env overwrites PATH with $GOROOT/bin, so the fake
			// compiler runs without /usr/bin and may use shell builtins
			// only -- `cat` here exits 127. A single-quoted string can
			// span newlines, so one `echo` reproduces any output that
			// contains no single quote.
			script := "#!/bin/sh\n"
			if tt.output != "" {
				script += "echo '" + strings.TrimSuffix(tt.output, "\n") + "'\n"
			}
			if err := os.WriteFile(path, []byte(script), 0o777); err != nil {
				t.Fatal(err)
			}

			c := Default()
			c.Compiler.Path = path
			err := c.CompilerInit()
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("CompilerInit() = %v, want %v", err, tt.wantErr)
			}
			// The two failure modes must stay distinguishable, or
			// callers cannot tell "no compiler output" from "output I
			// could not parse".
			if tt.wantErr == ErrVersionSyntax && errors.Is(err, ErrNoVersionString) {
				t.Errorf("CompilerInit() = %v, must not also match ErrNoVersionString", err)
			}
			if tt.wantErr == ErrNoVersionString && errors.Is(err, ErrVersionSyntax) {
				t.Errorf("CompilerInit() = %v, must not also match ErrVersionSyntax", err)
			}
		})
	}
}

// TestCompilerInitMissingBinaryIsNotNoVersion pins the reason these sentinels
// are gobusybox's own. A missing compiler binary makes CompilerInit return an
// error matching os.ErrNotExist; when "no version line" was also reported as
// os.ErrNotExist, the two were indistinguishable to callers.
func TestCompilerInitMissingBinaryIsNotNoVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake compiler is a shell script")
	}

	c := Default()
	c.Compiler.Path = filepath.Join(t.TempDir(), "definitely-not-here")
	missing := c.CompilerInit()
	if missing == nil {
		t.Fatal("CompilerInit() with a missing compiler = nil, want an error")
	}
	if !errors.Is(missing, os.ErrNotExist) {
		t.Errorf("CompilerInit() with a missing compiler = %v, want it to match os.ErrNotExist", missing)
	}
	if errors.Is(missing, ErrNoVersionString) {
		t.Errorf("CompilerInit() with a missing compiler = %v, must not match ErrNoVersionString", missing)
	}
	if !strings.Contains(missing.Error(), c.Compiler.Path) {
		t.Errorf("CompilerInit() = %v, want the message to name %q", missing, c.Compiler.Path)
	}

	// The other half of the pair: a compiler that runs but prints nothing
	// must not look like a missing binary.
	path := filepath.Join(t.TempDir(), "fakego")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho ''\n"), 0o777); err != nil {
		t.Fatal(err)
	}
	d := Default()
	d.Compiler.Path = path
	silent := d.CompilerInit()
	if !errors.Is(silent, ErrNoVersionString) {
		t.Errorf("CompilerInit() with a silent compiler = %v, want ErrNoVersionString", silent)
	}
	if errors.Is(silent, os.ErrNotExist) {
		t.Errorf("CompilerInit() with a silent compiler = %v, must not match os.ErrNotExist", silent)
	}
}

// TestCompilerInitAbsError checks that CompilerInit reports a compiler path it
// cannot resolve. compilerAbs's error used to be discarded, so the failure
// surfaced later as a fork/exec error from a process that should never have
// been started.
func TestCompilerInitAbsError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on the unix executable bit")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the executable bit")
	}

	// Readable and syntactically fine, but not executable.
	path := filepath.Join(t.TempDir(), "notexec")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'go version go1.22.4 linux/amd64'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := Default()
	c.Compiler.Path = path
	err := c.CompilerInit()
	if !errors.Is(err, os.ErrPermission) {
		t.Errorf("CompilerInit() with a non-executable compiler = %v, want it to match os.ErrPermission", err)
	}
	// os.ErrPermission alone does not prove the fix: discarding compilerAbs's
	// error still yields it, just from fork/exec after starting a process that
	// should never have been started. Path resolution reports *exec.Error;
	// fork/exec reports *os.PathError. Requiring the former is what
	// distinguishes a propagated error from a swallowed one.
	var lookErr *exec.Error
	if !errors.As(err, &lookErr) {
		t.Errorf("CompilerInit() = %v, want path resolution to report it (*exec.Error)", err)
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		t.Errorf("CompilerInit() = %v, must fail before spawning the compiler", err)
	}
	// Whatever the wrapping says, the message has to name the path that
	// failed, or the user cannot act on it.
	if !strings.Contains(err.Error(), path) {
		t.Errorf("CompilerInit() = %v, want the message to name %q", err, path)
	}
	if c.Compiler.IsInit {
		t.Error("CompilerInit() returned an error but marked the compiler initialised")
	}
}

// TestCompilerInitAbsolutisesPath pins compilerAbs's postcondition.
//
// exec.LookPath only promises an absolute result for a bare name resolved
// against PATH; a path containing a slash is tried directly and returned
// verbatim, so "./tinygo" stays relative with no error. compilerCmd runs the
// compiler with cmd.Dir set, so without filepath.Abs a relative -compiler
// value resolves against the build directory instead of the one the user
// named it in.
func TestCompilerInitAbsolutisesPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake compiler is a shell script")
	}

	// The compiler lives in the working directory; the build runs elsewhere.
	home, build := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "fakego"), []byte("#!/bin/sh\necho 'go version go1.22.4 linux/amd64'\n"), 0o777); err != nil {
		t.Fatal(err)
	}
	t.Chdir(home)

	c := Default()
	// Contains a slash, so LookPath returns it verbatim rather than
	// absolute.
	c.Compiler.Path = "./fakego"
	// Dir is what makes it bite: without filepath.Abs the compiler is
	// looked for inside the build directory, where it does not exist.
	c.Dir = build

	if err := c.CompilerInit(); err != nil {
		t.Fatalf("CompilerInit() with a relative compiler path = %v, want success", err)
	}
	if !filepath.IsAbs(c.Compiler.Path) {
		t.Errorf("Compiler.Path = %q, want it resolved to an absolute path", c.Compiler.Path)
	}
	if c.Compiler.Version != "go1.22.4" {
		t.Errorf("Compiler.Version = %q, want %q", c.Compiler.Version, "go1.22.4")
	}
}
