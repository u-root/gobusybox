// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bbinternal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// embedsItsOwnDocFile is a module whose library embeds one of its own Go
// files, the shape golang.org/x/tools/go/analysis/passes/inline has.
func embedsItsOwnDocFile(t *testing.T) string {
	t.Helper()

	return writeModule(t, map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.21\n",
		"cmd/c/main.go": `package main

import "example.com/m/pkg/lib"

func main() { println(lib.Doc) }
`,
		"pkg/lib/lib.go": `package lib

import _ "embed"

//go:embed doc.go
var Doc string
`,
		"pkg/lib/doc.go": `package lib

// The analyzer's documentation lives in this file and is embedded above.
`,
	})
}

// readOnlySources makes every file under dir read-only, which is how the
// module cache a build reads from stores them. Directories stay writable so
// the test's own cleanup can unlink the files.
func readOnlySources(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("Could not read %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := os.Chmod(filepath.Join(dir, e.Name()), 0o444); err != nil {
			t.Fatalf("Could not make %s read-only: %v", e.Name(), err)
		}
	}
}

// A Go file a package also embeds is written once, from the package's own
// syntax, rather than copied verbatim first.
//
// Copying it first put a read-only module cache file where the rewritten one
// goes, and writing a package is how every build puts its dependencies in
// place. One such package anywhere in the graph failed the whole build with
// "permission denied".
func TestWritePkgRewritesAGoFileThePackageAlsoEmbeds(t *testing.T) {
	dir := embedsItsOwnDocFile(t)
	readOnlySources(t, filepath.Join(dir, "pkg", "lib"))

	pkg := load(t, dir, "./pkg/lib")
	if len(pkg.EmbedFiles) != 1 {
		t.Fatalf("The fixture embeds %d files, want 1: the test proves nothing without one", len(pkg.EmbedFiles))
	}

	destDir := filepath.Join(t.TempDir(), "out")
	if err := WritePkg(pkg, destDir); err != nil {
		t.Fatalf("WritePkg: %v", err)
	}

	// The embed reads the file at compile time, so it has to hold the source
	// that was written rather than nothing at all.
	doc, err := os.ReadFile(filepath.Join(destDir, "doc.go"))
	if err != nil {
		t.Fatalf("Could not read the written doc.go: %v", err)
	}
	if !strings.Contains(string(doc), "package lib") {
		t.Errorf("The written doc.go is %q, want it to declare package lib", doc)
	}
}

// An embedded file that is not one of the package's own Go files is still
// copied, which is the case the fix must not break.
func TestWritePkgCopiesAnEmbeddedFileItDoesNotWrite(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.21\n",
		"cmd/c/main.go": `package main

import "example.com/m/pkg/lib"

func main() { println(lib.Data) }
`,
		"pkg/lib/lib.go": `package lib

import _ "embed"

//go:embed data/payload.txt
var Data string
`,
		"pkg/lib/data/payload.txt": "payload\n",
	})
	readOnlySources(t, filepath.Join(dir, "pkg", "lib"))
	readOnlySources(t, filepath.Join(dir, "pkg", "lib", "data"))

	destDir := filepath.Join(t.TempDir(), "out")
	if err := WritePkg(load(t, dir, "./pkg/lib"), destDir); err != nil {
		t.Fatalf("WritePkg: %v", err)
	}

	payload, err := os.ReadFile(filepath.Join(destDir, "data", "payload.txt"))
	if err != nil {
		t.Fatalf("Could not read the copied payload: %v", err)
	}
	if string(payload) != "payload\n" {
		t.Errorf("The copied payload is %q, want %q", payload, "payload\n")
	}
}
