// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lazyinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// loadTree writes a module and loads the package at pattern, as a build
// loads it.
//
// A real load rather than a hand-built package, because what is under test is
// what go/types inferred for a variable with no written type. A fixture that
// supplied the type would be asserting that this file agrees with itself.
func loadTree(t *testing.T, files map[string]string, pattern string) *packages.Package {
	t.Helper()

	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("Could not create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("Could not write %s: %v", path, err)
		}
	}

	pkgs, err := packages.Load(&packages.Config{
		Mode: loadMode,
		Dir:  dir,
		Env:  append(os.Environ(), "GO111MODULE=on", "GOWORK=off"),
	}, pattern)
	if err != nil {
		t.Fatalf("Could not load %s: %v", pattern, err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("Loading %s gave %d packages, want 1", pattern, len(pkgs))
	}
	if len(pkgs[0].Errors) > 0 {
		t.Fatalf("Loading %s: %v", pattern, pkgs[0].Errors)
	}
	return pkgs[0]
}

// libModule is a module whose pkg/lib holds the declaration under test, over
// a pkg/other that exports a constructor returning an unexported type.
func libModule(libSrc string) map[string]string {
	return map[string]string{
		"go.mod":        "module example.com/m\n\ngo 1.21\n",
		"cmd/c/main.go": "package main\n\nimport _ \"example.com/m/pkg/lib\"\n\nfunc main() {}\n",
		"pkg/other/other.go": `package other

type client struct{ N int }

type Exported struct{ N int }

func New() *client { return &client{} }

func NewExported() *Exported { return &Exported{} }

func Pair() (*client, error) { return &client{}, nil }
`,
		"pkg/lib/lib.go": libSrc,
	}
}

// Hoisting a variable has to write down the type that was inferred for it,
// and there is no way to write an unexported type from another package. Go
// offers no declaration that names a type without naming it, so the only
// answers are to leave the package alone or to emit source that does not
// build. Leaving it alone is the one that can be reported.
func TestAnUnnameableVariableTypeRefusesLaziness(t *testing.T) {
	for _, tt := range []struct {
		name string
		decl string
		want bool
	}{
		{"a pointer to an unexported type", "var V = other.New()", true},
		{"an unexported type in a slice", "var V = []any{}\nvar W = append([]*struct{}{}, nil)\nvar X = other.New()", true},
		{"one of several results", "var V, E = other.Pair()", true},
		{"an exported type", "var V = other.NewExported()", false},
		{"a standard library type", "var V = make(map[string]int)", false},
		{"a written type", "var V *struct{} = nil\nvar W = other.NewExported()", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			src := "package lib\n\nimport \"example.com/m/pkg/other\"\n\n" + tt.decl + "\n\nvar _ = other.NewExported\n"
			p := loadTree(t, libModule(src), "./pkg/lib")

			if got := hasUnnameableVarType(p); got != tt.want {
				t.Errorf("hasUnnameableVarType = %v, want %v, for:\n%s", got, tt.want, src)
			}
			refused := strings.Contains(scan(p).refusal, "cannot be named")
			if refused != tt.want {
				t.Errorf("scan refusal = %q, want refused=%v", scan(p).refusal, tt.want)
			}
		})
	}
}

// The blank identifier defines nothing, so go/types records no object for it.
// Reading its type would be a nil dereference, and skipping it is correct
// besides: `var _ = f()` declares nothing that has to be written down.
func TestABlankVariableIsNotAnUnnameableType(t *testing.T) {
	src := `package lib

import "example.com/m/pkg/other"

var _ = other.New()

var V = other.NewExported()
`
	p := loadTree(t, libModule(src), "./pkg/lib")

	if hasUnnameableVarType(p) {
		t.Error("hasUnnameableVarType = true for a blank variable, want false: it declares nothing to write down")
	}
}

// A package's own unexported types are nameable from inside it, which is
// where the declaration is written.
func TestAnUnexportedTypeOfItsOwnIsNameable(t *testing.T) {
	src := `package lib

type mine struct{ N int }

func make1() *mine { return &mine{} }

var V = make1()
`
	p := loadTree(t, libModule(src), "./pkg/lib")

	if hasUnnameableVarType(p) {
		t.Error("hasUnnameableVarType = true, want false: the type belongs to this package")
	}
}
