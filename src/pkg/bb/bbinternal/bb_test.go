// Copyright 2015-2019 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bbinternal

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestAmbiguousImportDir(t *testing.T) {
	for _, tt := range []struct {
		name    string
		pkgPath string
		errs    []packages.Error
		wantDir string // relative dir name (we check suffix)
		wantNil bool   // expect empty string (not an ambiguous import)
	}{
		{
			name:    "not an ambiguous import error",
			pkgPath: "example.com/foo",
			errs: []packages.Error{
				{Msg: "build constraints exclude all Go files"},
			},
			wantNil: true,
		},
		{
			name:    "ambiguous import prefers sub-module",
			pkgPath: "google.golang.org/genproto/googleapis/rpc/status",
			errs: []packages.Error{
				{
					Msg: "ambiguous import: found package google.golang.org/genproto/googleapis/rpc/status in multiple modules:\n" +
						"\tgoogle.golang.org/genproto v0.0.0-20230410155749-daa745c078e1 (/mod/genproto@v0.0.0-20230410155749-daa745c078e1/googleapis/rpc/status)\n" +
						"\tgoogle.golang.org/genproto/googleapis/rpc v0.0.0-20260401024825-9d38bb4040a9 (/mod/genproto/googleapis/rpc@v0.0.0-20260401024825-9d38bb4040a9/status)",
				},
			},
			// Should prefer the sub-module (longer prefix)
			wantDir: "/mod/genproto/googleapis/rpc@v0.0.0-20260401024825-9d38bb4040a9/status",
		},
		{
			name:    "ambiguous import falls back to first when no prefix match",
			pkgPath: "example.com/pkg",
			errs: []packages.Error{
				{
					Msg: "ambiguous import: found package example.com/pkg in multiple modules:\n" +
						"\texample.com/other v1.0.0 (/mod/other@v1.0.0/pkg)\n" +
						"\texample.com/another v2.0.0 (/mod/another@v2.0.0/pkg)",
				},
			},
			// Neither module path is a prefix of the pkg path; fall back to first
			wantDir: "/mod/other@v1.0.0/pkg",
		},
		{
			name:    "ambiguous import with empty error list",
			pkgPath: "example.com/foo",
			errs:    []packages.Error{},
			wantNil: true,
		},
		{
			name:    "ambiguous import first error not ambiguous second is",
			pkgPath: "example.com/a/b",
			errs: []packages.Error{
				{Msg: "some other error"},
				{
					Msg: "ambiguous import: found package example.com/a/b in multiple modules:\n" +
						"\texample.com/a v1.0.0 (/mod/a@v1.0.0/b)\n" +
						"\texample.com/a/b v1.0.0 (/mod/a_b@v1.0.0)",
				},
			},
			// Should find the ambiguous import in the second error
			wantDir: "/mod/a_b@v1.0.0",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ambiguousImportDir(tt.pkgPath, tt.errs)
			if err != nil {
				t.Fatalf("ambiguousImportDir(%q, ...) returned error: %v", tt.pkgPath, err)
			}
			if tt.wantNil {
				if got != "" {
					t.Errorf("ambiguousImportDir(%q, ...) = %q, want empty", tt.pkgPath, got)
				}
				return
			}
			if got != tt.wantDir {
				t.Errorf("ambiguousImportDir(%q, ...) = %q, want %q", tt.pkgPath, got, tt.wantDir)
			}
		})
	}
}

func TestCopyDirGoFiles(t *testing.T) {
	// Create a source directory with various files.
	srcDir := t.TempDir()
	destDir := t.TempDir()

	files := map[string]string{
		"foo.go":      "package foo\n",
		"bar.go":      "package foo\n",
		"foo_test.go": "package foo_test\n",
		"foo.s":       "// assembly\n",
		"README.md":   "readme\n",
		"data.json":   "{}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := copyDirGoFiles(srcDir, destDir); err != nil {
		t.Fatalf("copyDirGoFiles: %v", err)
	}

	// Check that Go source files and assembly files were copied, but not test
	// files or other files.
	wantCopied := map[string]bool{
		"foo.go": true,
		"bar.go": true,
		"foo.s":  true,
	}
	wantNotCopied := []string{"foo_test.go", "README.md", "data.json"}

	for name := range wantCopied {
		path := filepath.Join(destDir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to be copied, but got error: %v", name, err)
		}
	}
	for _, name := range wantNotCopied {
		path := filepath.Join(destDir, name)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("expected %s not to be copied, but it was", name)
		}
	}
}
