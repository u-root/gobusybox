// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bbinternal

import (
	"go/parser"
	"go/token"
	"os"
	gopath "path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// loadMode is the mode findpkg loads commands with. The rewrite reads
// TypesInfo.InitOrder and the package scope, so a hand-built package proves
// nothing here: only the go tool fills those in, and what it fills them with
// is the thing being rewritten.
const loadMode = packages.NeedName | packages.NeedImports | packages.NeedFiles |
	packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax |
	packages.NeedTypesInfo | packages.NeedCompiledGoFiles | packages.NeedModule |
	packages.NeedEmbedFiles

// writeModule writes a self-contained module and returns its directory.
//
// It requires nothing but the standard library, so these tests need no
// network and no populated module cache and cannot fail for a reason that has
// nothing to do with the rewrite.
func writeModule(t *testing.T, files map[string]string) string {
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
	return dir
}

// load returns the package at pattern, resolved inside dir.
func load(t *testing.T, dir, pattern string) *packages.Package {
	t.Helper()

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

// oneLibrary is a module holding a single library with both kinds of
// initialization, a package-level variable and an init body, plus the command
// that imports it so that the library is loaded the way a build loads it.
func oneLibrary(t *testing.T, librarySrc string) string {
	t.Helper()

	return writeModule(t, map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.21\n",
		"cmd/c/main.go": `package main

import "example.com/m/pkg/lib"

func main() { println(lib.V) }
`,
		"pkg/lib/lib.go": librarySrc,
	})
}

const plainLibrary = `package lib

var V = "var"

var FromInit string

func init() { FromInit = "init" }
`

// rewriteLibrary rewrites the library at pkgPath and returns its generated
// bbinit.go.
func rewriteLibrary(t *testing.T, dir, pkgPath string, deps []Hook) string {
	t.Helper()

	lib := NewLibrary(load(t, dir, pkgPath))
	dest := filepath.Join(t.TempDir(), "out")
	if err := lib.RewriteLibrary(dest, deps); err != nil {
		t.Fatalf("RewriteLibrary: %v", err)
	}
	hook, err := os.ReadFile(filepath.Join(dest, "bbinit.go"))
	if err != nil {
		t.Fatalf("Could not read the generated hook: %v", err)
	}
	return string(hook)
}

// A library's initialization moves into its hook: its package-level variables
// first and its init bodies after, which is the order Go ran them in.
//
// Deferring one without the other is not a smaller step but a broken one. Go
// finishes an imported package's initialization in full before the importing
// package's variables run, so a variable left eager would read a package that
// had not been initialized.
func TestALibraryHookRunsVariablesThenInitBodies(t *testing.T) {
	hook := rewriteLibrary(t, oneLibrary(t, plainLibrary), "./pkg/lib", nil)

	calls := callsIn(t, hook, "BusyboxInit")
	if len(calls) != 2 {
		t.Fatalf("BusyboxInit makes %d calls (%v), want the variables and the init body", len(calls), calls)
	}
	// busyboxInit0 holds the hoisted variables and is created first, so a
	// lower number is the earlier of the two whatever the collision check
	// did to the names.
	if calls[0] != "busyboxInit0" {
		t.Errorf("BusyboxInit calls %v, want the hoisted variables (busyboxInit0) first", calls)
	}
}

// The hook runs once however many paths reach it. A package in a diamond is
// initialized once by Go, and has to be here too.
func TestALibraryHookRunsOnce(t *testing.T) {
	hook := rewriteLibrary(t, oneLibrary(t, plainLibrary), "./pkg/lib", nil)

	if !strings.Contains(hook, "if busyboxInitDone {\n\t\treturn\n\t}") {
		t.Errorf("The generated hook has no once-only guard:\n%s", hook)
	}
	if !strings.Contains(hook, "busyboxInitDone = true") {
		t.Errorf("The generated hook never sets its guard, so it would run every time:\n%s", hook)
	}
}

// A hook calls its direct imports' hooks before anything of its own, because
// Go initialized them first.
func TestALibraryHookCallsItsImportsFirst(t *testing.T) {
	hook := rewriteLibrary(t, oneLibrary(t, plainLibrary), "./pkg/lib", []Hook{
		{ImportPath: "example.com/other/a", FuncName: "BusyboxInit"},
		{ImportPath: "example.com/other/b", FuncName: "BusyboxInit"},
	})

	calls := callsIn(t, hook, "BusyboxInit")
	if len(calls) < 3 {
		t.Fatalf("BusyboxInit makes %d calls (%v), want two imports and this package's own", len(calls), calls)
	}
	for i, want := range []string{"bbdep0.BusyboxInit", "bbdep1.BusyboxInit"} {
		if calls[i] != want {
			t.Errorf("BusyboxInit calls %v; call %d is %q, want %q", calls, i, calls[i], want)
		}
	}
	for _, path := range []string{`"example.com/other/a"`, `"example.com/other/b"`} {
		if !strings.Contains(hook, path) {
			t.Errorf("The generated hook does not import %s:\n%s", path, hook)
		}
	}
}

// The hook's name is chosen against the library's own scope. A library is
// third-party source and may already have a function called BusyboxInit;
// generating a second one would not compile.
func TestALibraryHookNameAvoidsACollision(t *testing.T) {
	hook := rewriteLibrary(t, oneLibrary(t, plainLibrary+`
// BusyboxInit is already taken by the library itself.
func BusyboxInit() {}
`), "./pkg/lib", nil)

	if !strings.Contains(hook, "func BusyboxInit0()") {
		t.Errorf("The generated hook did not avoid the name the library already uses:\n%s", hook)
	}
}

// So is the guard's, for the same reason and separately: a library may have
// one of the two names and not the other.
func TestALibraryGuardNameAvoidsACollision(t *testing.T) {
	hook := rewriteLibrary(t, oneLibrary(t, plainLibrary+`
// busyboxInitDone is already taken by the library itself.
var busyboxInitDone = "not a bool"
`), "./pkg/lib", nil)

	if !strings.Contains(hook, "var busyboxInitDone0 bool") {
		t.Errorf("The generated guard did not avoid the name the library already uses:\n%s", hook)
	}
}

// And so are the import aliases. They are generated names in a file that did
// not exist, but the package scope they sit in is the library's.
func TestALibraryHookImportAliasAvoidsACollision(t *testing.T) {
	hook := rewriteLibrary(t, oneLibrary(t, plainLibrary+`
// bbdep0 is already taken by the library itself.
func bbdep0() {}
`), "./pkg/lib", []Hook{
		{ImportPath: "example.com/other/a", FuncName: "BusyboxInit"},
	})

	if !strings.Contains(hook, "bbdep00 ") {
		t.Errorf("The generated import alias did not avoid the name the library already uses:\n%s", hook)
	}
}

// A library keeps its package name. Everything that imports it goes on
// importing it by the path it already had, and those import statements are
// not rewritten; only a command is renamed, because its package is main.
func TestALibraryKeepsItsPackageName(t *testing.T) {
	dir := oneLibrary(t, plainLibrary)
	lib := NewLibrary(load(t, dir, "./pkg/lib"))
	dest := filepath.Join(t.TempDir(), "out")
	if err := lib.RewriteLibrary(dest, nil); err != nil {
		t.Fatalf("RewriteLibrary: %v", err)
	}

	for _, name := range []string{"lib.go", "bbinit.go"} {
		src, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("Could not read the rewritten %s: %v", name, err)
		}
		if !strings.HasPrefix(packageClause(t, string(src)), "package lib") {
			t.Errorf("The rewritten %s declares %q, want package lib", name, packageClause(t, string(src)))
		}
	}
}

// Rewriting a command as a library is a programming error, and saying so is
// worth a check of its own.
//
// It fails either way: a command has no hook name, so the generated file
// declares `func ()` and the formatter rejects it. But that error reads
//
//	bad parse while processing imports ".../bbinit.go":
//	commandline:8:10: expected type, found newline
//
// which names the generated file rather than the mistake, and sends whoever
// reads it to look for a defect in the code generator. Asserting only that an
// error came back would pass without the check being there at all.
func TestRewriteLibraryRefusesACommand(t *testing.T) {
	dir := oneLibrary(t, plainLibrary)
	cmd := NewPackage("c", load(t, dir, "./cmd/c"))

	err := cmd.RewriteLibrary(filepath.Join(t.TempDir(), "out"), nil)
	if err == nil {
		t.Fatal("RewriteLibrary on a command returned no error, want one")
	}
	if !strings.Contains(err.Error(), "was not created as a library") {
		t.Errorf("RewriteLibrary on a command said %q, want it to name the mistake", err)
	}
}

// callsIn returns the function calls made in the named function of src, in
// order, as they are written.
func callsIn(t *testing.T, src, prefix string) []string {
	t.Helper()

	var body string
	for _, chunk := range strings.Split(src, "\nfunc "+prefix) {
		if i := strings.Index(chunk, "{\n"); i >= 0 && strings.HasPrefix(strings.TrimSpace(chunk), "(") {
			body = chunk[i:]
		}
	}
	if body == "" {
		t.Fatalf("No function starting %q in:\n%s", prefix, src)
	}
	call := regexp.MustCompile(`(?m)^\t([A-Za-z_][\w.]*)\(\)$`)
	var out []string
	for _, m := range call.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

// packageClause returns src's package declaration.
func packageClause(t *testing.T, src string) string {
	t.Helper()

	for line := range strings.SplitSeq(src, "\n") {
		if strings.HasPrefix(line, "package ") {
			return line
		}
	}
	t.Fatalf("No package clause in:\n%s", src)
	return ""
}

// A package may have a file called bbinit.go of its own. Writing the hook
// over it loses whatever was there, and quietly: WritePkg has already written
// the package's real files by the time the hook is written, and the hoisted
// variable initializers are appended to whichever file sorts first, which
// "bbinit.go" very often does. The build then fails with `undefined:
// busyboxInit0`, which names neither the file nor the collision.
func TestTheGeneratedFileDoesNotOverwriteOneOfThePackagesOwn(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":        "module example.com/m\n\ngo 1.21\n",
		"cmd/c/main.go": "package main\n\nimport \"example.com/m/pkg/lib\"\n\nfunc main() { println(lib.Precious()) }\n",
		"pkg/lib/lib.go": `package lib

var V = "var"

func init() { V += "-init" }
`,
		// Sorts before lib.go, which is what makes this bite.
		"pkg/lib/bbinit.go": `package lib

// Precious is a real source file that happens to have this name.
func Precious() string { return "precious" }
`,
	})

	lib := NewLibrary(load(t, dir, "./pkg/lib"))
	dest := filepath.Join(t.TempDir(), "out")
	if err := lib.RewriteLibrary(dest, nil); err != nil {
		t.Fatalf("RewriteLibrary: %v", err)
	}

	own, err := os.ReadFile(filepath.Join(dest, "bbinit.go"))
	if err != nil {
		t.Fatalf("Could not read the package's own bbinit.go: %v", err)
	}
	if !strings.Contains(string(own), "Precious") {
		t.Errorf("The package's own bbinit.go was overwritten:\n%s", own)
	}

	// The hook went somewhere, and it is not that file.
	found := ""
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		src, err := os.ReadFile(filepath.Join(dest, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "func "+lib.HookName()+"()") {
			found = e.Name()
		}
	}
	if found == "" {
		t.Errorf("The hook was not written at all; the directory holds %v", names(entries))
	}
	if found == "bbinit.go" {
		t.Error("The hook was written over the package's own bbinit.go")
	}
}

// Two inferred variable types naming different packages that call themselves
// the same thing. go/types' scopes describe the source as it was loaded and
// know nothing of an import this rewrite has already added, so without
// recording them both would be offered "foo":
//
//	lib.go:4:2: other declaration of foo
//	lib.go:5:2: "example.com/b/foo" imported and not used
func TestTwoAddedImportsNamedTheSameGetDifferentNames(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":           "module example.com/m\n\ngo 1.21\n",
		"cmd/c/main.go":    "package main\n\nimport \"example.com/m/pkg/lib\"\n\nfunc main() { println(lib.X.N, lib.Y.M) }\n",
		"pkg/a/foo/foo.go": "package foo\n\ntype T struct{ N int }\n",
		"pkg/b/foo/foo.go": "package foo\n\ntype U struct{ M int }\n",
		"pkg/mk/mk.go": `package mk

import (
	afoo "example.com/m/pkg/a/foo"
	bfoo "example.com/m/pkg/b/foo"
)

func MakeT() *afoo.T { return &afoo.T{N: 1} }
func MakeU() *bfoo.U { return &bfoo.U{M: 2} }
`,
		// Neither a/foo nor b/foo is imported here, so the rewrite has
		// to add both, and both are called foo.
		"pkg/lib/lib.go": `package lib

import "example.com/m/pkg/mk"

var X = mk.MakeT()
var Y = mk.MakeU()
`,
	})

	lib := NewLibrary(load(t, dir, "./pkg/lib"))
	dest := filepath.Join(t.TempDir(), "out")
	if err := lib.RewriteLibrary(dest, nil); err != nil {
		t.Fatalf("RewriteLibrary: %v", err)
	}

	src, err := os.ReadFile(filepath.Join(dest, "lib.go"))
	if err != nil {
		t.Fatal(err)
	}
	// Both packages have to be imported, and under names that differ.
	for _, path := range []string{`"example.com/m/pkg/a/foo"`, `"example.com/m/pkg/b/foo"`} {
		if !strings.Contains(string(src), path) {
			t.Errorf("The rewritten file does not import %s:\n%s", path, src)
		}
	}
	if names := importNames(t, string(src)); len(names) != len(unique(names)) {
		t.Errorf("The rewritten file declares an import name twice (%v):\n%s", names, src)
	}
}

// `var _ = f()` declares nothing, so the rewrite leaves no declaration
// behind: the assignment keeps the side effect. Writing `var _ T` out is at
// best noise, and unbuildable when T is a type this file cannot name.
func TestABlankVariableLeavesNoDeclaration(t *testing.T) {
	dir := writeModule(t, map[string]string{
		"go.mod":        "module example.com/m\n\ngo 1.21\n",
		"cmd/c/main.go": "package main\n\nimport _ \"example.com/m/pkg/lib\"\n\nfunc main() {}\n",
		"pkg/lib/lib.go": `package lib

import "fmt"

var _ = fmt.Sprint("side effect")

var V = "kept"
`,
	})

	lib := NewLibrary(load(t, dir, "./pkg/lib"))
	dest := filepath.Join(t.TempDir(), "out")
	if err := lib.RewriteLibrary(dest, nil); err != nil {
		t.Fatalf("RewriteLibrary: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(dest, "lib.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "var _ ") {
		t.Errorf("The rewrite left a declaration for a blank variable:\n%s", src)
	}
	if !strings.Contains(string(src), `_ = fmt.Sprint("side effect")`) {
		t.Errorf("The rewrite dropped the blank variable's side effect:\n%s", src)
	}
	if !strings.Contains(string(src), "var V string") {
		t.Errorf("The rewrite lost a real variable:\n%s", src)
	}
}

func names(entries []os.DirEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// importNames returns the local name of every import in src, as written.
func importNames(t *testing.T, src string) []string {
	t.Helper()

	f, err := parser.ParseFile(token.NewFileSet(), "src.go", src, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("Could not parse the rewritten file: %v", err)
	}
	var out []string
	for _, imp := range f.Imports {
		if imp.Name != nil {
			out = append(out, imp.Name.Name)
			continue
		}
		path, _ := strconv.Unquote(imp.Path.Value)
		out = append(out, gopath.Base(path))
	}
	return out
}

func unique(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
