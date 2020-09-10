// Package monoimporter provides a monorepo-compatible types.Importer for Go
// packages.
package monoimporter

import (
	"archive/zip"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/gcexportdata"
	"golang.org/x/tools/go/packages"
)

type finder interface {
	findAndOpen(pkg string) io.ReadCloser
}

func find(finders []finder, pkg string) io.ReadCloser {
	for _, f := range finders {
		if file := f.findAndOpen(pkg); file != nil {
			return file
		}
	}
	return nil
}

// zipReader exists only for the internal blaze Go toolchain, which is packaged
// in a big zip.
type zipReader struct {
	ctxt   build.Context
	stdlib *zip.Reader
	files  map[string]*zip.File
}

func newZipReader(stdlib *zip.Reader, ctxt build.Context) *zipReader {
	z := &zipReader{
		stdlib: stdlib,
		files:  make(map[string]*zip.File),
		ctxt:   ctxt,
	}
	for _, file := range z.stdlib.File {
		z.files[file.Name] = file
	}
	return z
}

// thatOneString is the Go build context directory name used by
// blaze/bazel/buck/Go.
//
// GOOS_GOARCH[_InstallSuffix], e.g. linux_amd64 or linux_amd64_pure.
func thatOneString(ctxt build.Context) string {
	var suffix string
	if len(ctxt.InstallSuffix) > 0 {
		suffix = fmt.Sprintf("_%s", ctxt.InstallSuffix)
	}
	return fmt.Sprintf("%s_%s%s", ctxt.GOOS, ctxt.GOARCH, suffix)
}

func (z *zipReader) findAndOpen(pkg string) io.ReadCloser {
	name := fmt.Sprintf("%s/%s.x", thatOneString(z.ctxt), pkg)
	f, ok := z.files[name]
	if !ok {
		return nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil
	}
	return rc
}

type archives struct {
	ctxt  build.Context
	archs []string

	// pkgs is a map of Go import path -> archive file.
	//
	// While in blaze, importPath == file path, in bazel each package gets
	// to define its own import path.
	pkgs map[string]string
}

func (a archives) findAndOpen(pkg string) io.ReadCloser {
	// In bazel, non-stdlib dependencies should be found through this,
	// because we pass an explicit map of import path -> archive path from
	// the Starlark rules.
	if filename, ok := a.pkgs[pkg]; ok {
		f, err := os.Open(filename)
		if err == nil {
			return f
		}
	}

	suffixes := []string{
		// bazel stdlib archives should be found using this method.
		fmt.Sprintf("/%s.x", pkg),
		fmt.Sprintf("/%s.a", pkg),

		// blaze finds non-stdlib dependency archives through this, and
		// uses a zip for the stdlib files.
		fmt.Sprintf("/%s.x", pkg),
		fmt.Sprintf("/%s.a", pkg),
	}

	for _, s := range a.archs {
		if fi, err := os.Stat(s); err == nil && fi.IsDir() {
			name := fmt.Sprintf("%s/%s.a", thatOneString(a.ctxt), pkg)
			f, err := os.Open(filepath.Join(s, name))
			if err == nil {
				return f
			}
		}
		for _, suffix := range suffixes {
			if strings.HasSuffix(s, suffix) {
				ar, err := os.Open(s)
				if err == nil {
					return ar
				}
			}
		}
	}
	return nil
}

// Importer implements a go/types.Importer for bazel-like monorepo build
// systems for Go packages.
//
// While open source Go depends on GOPATH and GOROOT to find packages,
// bazel-like build systems such as blaze or buck rely on a monorepo-style
// package search instead of using GOPATH and standard library packages are
// found in a zipped archive instead of GOROOT.
type Importer struct {
	fset *token.FileSet

	// imports is a cache of imported packages.
	imports map[string]*types.Package

	// archives is a list of paths to compiled Go package archives.
	archives archives

	// stdlib is an archive reader for standard library package object
	// files.
	stdlib *zipReader
}

// NewFromZips returns a new monorepo importer, using the build context to pick
// the desired standard library zip archive.
//
// zips refers to zip file paths with Go standard library object files.
//
// archives refers to directories in which to find compiled Go package object files.
func NewFromZips(ctxt build.Context, archives []string, zips []string) (*Importer, error) {
	// Some architectures have extra stuff after the GOARCH in the stdlib filename.
	ctxtWithWildcard := ctxt
	ctxtWithWildcard.GOARCH += "*"

	var stdlib *zip.Reader
	wantPattern := fmt.Sprintf("%s.x.zip", thatOneString(ctxtWithWildcard))
	for _, dir := range zips {
		if matched, err := filepath.Match(wantPattern, filepath.Base(dir)); err != nil {
			log.Printf("Error with pattern %q: %v", wantPattern, err)
		} else if matched {
			stdlibZ, err := zip.OpenReader(dir)
			if err != nil {
				return nil, err
			}
			stdlib = &stdlibZ.Reader
			break
		}
	}

	// Reverse-sort the archives so that *.x is listed before *.a in
	// blaze-based archive searching.
	//
	// bazel Go rules only pass *.a, which is fine for bazel.
	// blaze Go rules pass both *.x and *.a, and we need to prefer *.x.
	sort.Sort(sort.Reverse(sort.StringSlice(archives)))
	return New(ctxt, archives, stdlib), nil
}

// New returns a new monorepo importer.
func New(ctxt build.Context, archs []string, stdlib *zip.Reader) *Importer {
	pkgs := make(map[string]string)
	var unnamedArchives []string
	for _, archive := range archs {
		nameAndFile := strings.Split(archive, ":")
		switch len(nameAndFile) {
		case 0:
			continue
		case 1:
			unnamedArchives = append(unnamedArchives, archive)
		case 2:
			pkgs[nameAndFile[0]] = nameAndFile[1]
		}
	}

	i := &Importer{
		imports: map[string]*types.Package{
			"unsafe": types.Unsafe,
		},
		fset: token.NewFileSet(),
		archives: archives{
			ctxt:  ctxt,
			archs: unnamedArchives,
			pkgs:  pkgs,
		},
	}
	if stdlib != nil {
		i.stdlib = newZipReader(stdlib, ctxt)
	}
	return i
}

// Import implements types.Importer.Import.
func (i *Importer) Import(importPath string) (*types.Package, error) {
	if pkg, ok := i.imports[importPath]; ok && pkg.Complete() {
		return pkg, nil
	}

	pkg := strings.TrimPrefix(importPath, "google3/")
	finders := []finder{i.archives}
	if i.stdlib != nil {
		finders = append(finders, i.stdlib)
	}
	file := find(finders, pkg)
	if file == nil {
		return nil, fmt.Errorf("package %q not found", importPath)
	}
	defer file.Close()

	r, err := gcexportdata.NewReader(file)
	if err != nil {
		return nil, err
	}
	return gcexportdata.Read(r, i.fset, i.imports, importPath)
}

// Load loads a google3 package.
func Load(pkgPath string, filepaths []string, importer types.Importer) (*packages.Package, error) {
	p := &packages.Package{
		PkgPath: pkgPath,
	}

	// If go_binary, bla, if go_library, bla
	fset, astFiles, parsedFileNames, err := ParseAST("main", filepaths)
	if err != nil {
		return nil, err
	}

	p.Fset = fset
	p.Syntax = astFiles
	p.CompiledGoFiles = parsedFileNames
	p.GoFiles = filepaths

	// Type-check the package before we continue. We need types to rewrite
	// some statements.
	conf := types.Config{
		Importer: importer,

		// We only need global declarations' types.
		IgnoreFuncBodies: true,
	}

	p.TypesInfo = &types.Info{
		// If you don't make these maps before passing TypesInfo to
		// Check, they won't be filled in.
		Types:  make(map[ast.Expr]types.TypeAndValue),
		Scopes: make(map[ast.Node]*types.Scope),
	}
	// It's important that p.Syntax be in the same order every time for
	// p.TypesInfo to be stable.
	tpkg, err := conf.Check(pkgPath, p.Fset, p.Syntax, p.TypesInfo)
	if err != nil {
		return nil, fmt.Errorf("type checking failed: %v", err)
	}
	p.Types = tpkg
	return p, nil
}

// ParseAST parses the given files for a package named main.
//
// Only files with a matching package statement will be part of the AST
// returned.
func ParseAST(name string, files []string) (*token.FileSet, []*ast.File, []string, error) {
	fset := token.NewFileSet()
	astFiles := make(map[string]*ast.File)
	for _, path := range files {
		if src, err := parser.ParseFile(fset, path, nil, parser.ParseComments); err == nil && src.Name.Name == name {
			astFiles[path] = src
		} else if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to parse AST in file %q: %v", path, err)
		}
	}

	// Did we parse anything?
	if len(astFiles) == 0 {
		return nil, nil, nil, fmt.Errorf("no valid `main` package files found in %v", files)
	}

	// The order of types.Info.InitOrder depends on this list of files
	// always being passed to conf.Check in the same order.
	sort.Strings(files)

	sortedFiles := make([]*ast.File, 0, len(astFiles))
	parsedFiles := make([]string, 0, len(astFiles))
	for _, name := range files {
		if f, ok := astFiles[name]; ok {
			sortedFiles = append(sortedFiles, f)
			parsedFiles = append(parsedFiles, name)
		}
	}
	return fset, sortedFiles, parsedFiles, nil
}
