// Copyright 2015-2019 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package bb builds one busybox-like binary out of many Go command sources.
//
// This allows you to take two Go commands, such as Go implementations of `sl`
// and `cowsay` and compile them into one binary, callable like `./bb sl` and
// `./bb cowsay`.
//
// Which command is invoked is determined by `argv[0]` or `argv[1]` if
// `argv[0]` is not recognized.
//
// Under the hood, bb implements a Go source-to-source transformation on pure
// Go code. This AST transformation does the following:
//
//   - Takes a Go command's source files and rewrites them into Go package files
//     without global side effects.
//   - Writes a `main.go` file with a `main()` that calls into the appropriate Go
//     command package based on `argv[0]`.
//
// Principally, the AST transformation moves all global side-effects into
// callable package functions. E.g. `main` becomes `Main`, each `init` becomes
// `InitN`, and global variable assignments are moved into their own `InitN`.
package bb

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"

	"github.com/u-root/gobusybox/src/pkg/golang"
	"github.com/u-root/u-root/pkg/cp"
)

func checkDuplicate(cmds []string) error {
	seen := make(map[string]struct{})
	for _, cmd := range cmds {
		name := path.Base(cmd)
		if _, ok := seen[name]; ok {
			return fmt.Errorf("failed to build with bb: found duplicate command %s", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

// BuildBusybox builds a busybox of the given Go packages.
//
// pkgs is a list of Go import paths. If nil is returned, binaryPath will hold
// the busybox-style binary.
func BuildBusybox(env golang.Environ, cmdPaths []string, noStrip bool, binaryPath string) (nerr error) {
	tmpDir, err := ioutil.TempDir("", "bb-")
	if err != nil {
		return err
	}
	defer func() {
		if nerr != nil {
			log.Printf("Preserving bb temporary directory at %s due to error", tmpDir)
		} else {
			os.RemoveAll(tmpDir)
		}
	}()

	// INB4: yes, this *is* too clever. It's because Go modules are too
	// clever. Sorry.
	//
	// Inevitably, we will build commands across multiple modules, e.g.
	// u-root and u-bmc each have their own go.mod, but will get built into
	// one busybox executable.
	//
	// Each u-bmc and u-root command need their respective go.mod
	// dependencies, so we'll preserve their module file.
	//
	// However, we *also* need to still allow building with GOPATH and
	// vendoring. The structure we build *has* to also resemble a
	// GOPATH-based build.
	//
	// The easiest thing to do is to make the directory structure
	// GOPATH-compatible, and use go.mods to replace modules with the local
	// directories.
	//
	// So pretend GOPATH=tmp.
	//
	// Structure we'll build:
	//
	//   tmp/src/bb
	//   tmp/src/bb/main.go
	//      import "<module1>/cmd/foo"
	//      import "<module2>/cmd/bar"
	//
	//      func main()
	//
	// The only way to refer to other Go modules locally is to replace them
	// with local paths in a top-level go.mod:
	//
	//   tmp/go.mod
	//      package bb.u-root.com
	//
	//	replace <module1> => ./src/<module1>
	//	replace <module2> => ./src/<module2>
	//
	// Because GOPATH=tmp, the source should be in $GOPATH/src, just to
	// accommodate both build systems.
	//
	//   tmp/src/<module1>
	//   tmp/src/<module1>/go.mod
	//   tmp/src/<module1>/cmd/foo/main.go
	//
	//   tmp/src/<module2>
	//   tmp/src/<module2>/go.mod
	//   tmp/src/<module2>/cmd/bar/main.go

	bbDir := filepath.Join(tmpDir, "src/bb")
	if err := os.MkdirAll(bbDir, 0755); err != nil {
		return err
	}
	pkgDir := filepath.Join(tmpDir, "src")

	// Collect all packages that we need to actually re-write.
	/*if err := checkDuplicate(cmdPaths); err != nil {
		return err
	}*/

	// Ask go about all the commands in one batch for dependency caching.
	cmds, err := NewPackages(env, cmdPaths...)
	if err != nil {
		return fmt.Errorf("finding packages failed: %v", err)
	}

	if len(cmds) == 0 {
		return fmt.Errorf("no commands compiled")
	}
	log.Printf("commands: %v", cmds)

	// List of packages to import in the real main file.
	var bbImports []string
	// Rewrite commands to packages.
	for _, cmd := range cmds {
		destination := filepath.Join(pkgDir, cmd.Pkg.PkgPath)

		if err := cmd.Rewrite(destination); err != nil {
			return fmt.Errorf("rewriting command %q failed: %v", cmd.Pkg.PkgPath, err)
		}
		bbImports = append(bbImports, cmd.Pkg.PkgPath)
	}

	if err := ioutil.WriteFile(filepath.Join(bbDir, "main.go"), bbMainSource, 0755); err != nil {
		return err
	}

	bbEnv := env
	// main.go has no outside dependencies, and the go.mod file has not
	// been written yet, so turn off Go modules.
	bbEnv.GO111MODULE = "off"
	bb, err := NewPackages(bbEnv, bbDir)
	if err != nil {
		return err
	}
	if len(bb) == 0 {
		return fmt.Errorf("bb package not found")
	}

	// Collect and write dependencies into pkgDir.
	hasModules, err := dealWithDeps(env, tmpDir, pkgDir, cmds)
	if err != nil {
		return fmt.Errorf("dealing with deps: %v", err)
	}

	// Create bb main.go.
	if err := CreateBBMainSource(bb[0].Pkg, bbImports, bbDir); err != nil {
		return fmt.Errorf("creating bb main() file failed: %v", err)
	}

	// We do not support non-module compilation anymore, because the u-root
	// dependencies need modules anyway. There's literally no way around
	// them.

	// Compile bb.
	if env.GO111MODULE == "off" || !hasModules {
		env.GOPATH = tmpDir
	}
	if err := env.BuildDir(bbDir, binaryPath, golang.BuildOpts{NoStrip: noStrip}); err != nil {
		return fmt.Errorf("go build: %v", err)
	}
	return nil
}

func dealWithDeps(env golang.Environ, tmpDir, pkgDir string, mainPkgs []*Package) (bool, error) {
	// Module-enabled Go programs resolve their dependencies in one of two ways:
	//
	// - locally, if the dependency is *in* the module
	// - remotely, if it is outside of the module
	//
	// I.e. if the module is github.com/u-root/u-root,
	//
	// - local: github.com/u-root/u-root/pkg/uio
	// - remote: github.com/hugelgupf/p9/p9
	// - also local: a remote module, with a local replace rule
	//
	// For remote dependencies, we copy the go.mod into the temporary directory.
	//
	// For local dependencies, we copy all dependency packages' files over.
	var localDepPkgs []*packages.Package
	var modules []string
	for _, p := range mainPkgs {
		// Find all dependency packages that are *within* module boundaries for this package.
		//
		// writeDeps also copies the go.mod into the right place.
		localDeps, modulePath, err := collectDeps(env, pkgDir, p.Pkg)
		if err != nil {
			return false, fmt.Errorf("resolving dependencies for %q failed: %v", p.Pkg.PkgPath, err)
		}
		localDepPkgs = append(localDepPkgs, localDeps...)
		if len(modulePath) > 0 {
			modules = append(modules, modulePath)
		}
	}

	// Copy local dependency packages into temporary module directories at
	// tmpDir/src.
	seenIDs := make(map[string]struct{})
	for _, p := range localDepPkgs {
		if _, ok := seenIDs[p.ID]; !ok {
			if err := writePkg(p, filepath.Join(pkgDir, p.PkgPath)); err != nil {
				return false, fmt.Errorf("writing package %s failed: %v", p, err)
			}
			seenIDs[p.ID] = struct{}{}
		}
	}

	// Avoid go.mod in the case of GO111MODULE=(auto|off) if there are no modules.
	if env.GO111MODULE == "on" || len(modules) > 0 {
		// go.mod for the bb binary.
		//
		// Add local replace rules for all modules we're compiling.
		//
		// This is the only way to locally reference another modules'
		// repository. Otherwise, go'll try to go online to get the source.
		//
		// The module name is something that'll never be online, lest Go
		// decides to go on the internet.
		content := `module bb.u-root.com`
		for _, mpath := range modules {
			content += fmt.Sprintf("\nreplace %s => ./src/%s\n", mpath, mpath)
		}
		if err := ioutil.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(content), 0755); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// deps recursively iterates through imports.
func deps(p *packages.Package, filter func(p *packages.Package) bool) []*packages.Package {
	imports := make(map[*packages.Package]struct{})
	for _, pkg := range p.Imports {
		if filter(pkg) {
			imports[pkg] = struct{}{}
		}
		// Non-filtered packages may depend on filtered packages.
		for _, deppkg := range deps(pkg, filter) {
			imports[deppkg] = struct{}{}
		}
	}
	var i []*packages.Package
	for p := range imports {
		i = append(i, p)
	}
	return i
}

func collectDeps(env golang.Environ, pkgDir string, p *packages.Package) ([]*packages.Package, string, error) {
	if p.Module != nil {
		if err := os.MkdirAll(filepath.Join(pkgDir, p.Module.Path), 0755); err != nil {
			return nil, "", err
		}

		// Use the module file for all outside dependencies.
		if err := cp.Copy(p.Module.GoMod, filepath.Join(pkgDir, p.Module.Path, "go.mod")); err != nil {
			return nil, "", err
		}

		// Collect all "local" dependency packages, to be copied into
		// the temporary directory structure later.
		dep := deps(p, func(pkg *packages.Package) bool {
			// Is this a dependency within the module?
			return strings.HasPrefix(pkg.ID, p.Module.Path)
		})
		return dep, p.Module.Path, nil
	}

	// If modules are not enabled, we need a copy of *ALL*
	// non-standard-library dependencies in the temporary directory.
	dep := deps(p, func(pkg *packages.Package) bool {
		// First component of package path contains a "."?
		//
		// Poor man's standard library test.
		firstComp := strings.SplitN(pkg.ID, "/", 2)
		return strings.Contains(firstComp[0], ".")
	})
	return dep, "", nil
}

// CreateBBMainSource creates a bb Go command that imports all given pkgs.
//
// p must be the bb template.
//
// - For each pkg in pkgs, add
//     import _ "pkg"
//   to astp's first file.
// - Write source file out to destDir.
func CreateBBMainSource(p *packages.Package, pkgs []string, destDir string) error {
	if len(p.Syntax) != 1 {
		return fmt.Errorf("bb cmd template is supposed to only have one file")
	}

	bbRegisterInit := &ast.FuncDecl{
		Name: ast.NewIdent("init"),
		Type: &ast.FuncType{},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{},
		},
	}

	for _, pkg := range pkgs {
		name := path.Base(pkg)
		// import mangledpkg "pkg"
		//
		// A lot of package names conflict with code in main.go or Go keywords (e.g. init cmd)
		astutil.AddNamedImport(p.Fset, p.Syntax[0], fmt.Sprintf("mangled%s", name), pkg)

		bbRegisterInit.Body.List = append(bbRegisterInit.Body.List, &ast.ExprStmt{X: &ast.CallExpr{
			Fun: ast.NewIdent("Register"),
			Args: []ast.Expr{
				// name=
				&ast.BasicLit{
					Kind:  token.STRING,
					Value: strconv.Quote(name),
				},
				// init=
				ast.NewIdent(fmt.Sprintf("mangled%s.Init", name)),
				// main=
				ast.NewIdent(fmt.Sprintf("mangled%s.Main", name)),
			},
		}})

	}

	p.Syntax[0].Decls = append(p.Syntax[0].Decls, bbRegisterInit)
	return writeFiles(destDir, p.Fset, p.Syntax)
}

// Package is a Go package.
type Package struct {
	// Name is the executable command name.
	//
	// In the standard Go tool chain, this is usually the base name of the
	// directory containing its source files.
	Name string

	// Pkg is the actual data about the package.
	Pkg *packages.Package

	// initCount keeps track of what the next init's index should be.
	initCount uint

	// init is the cmd.Init function that calls all other InitXs in the
	// right order.
	init *ast.FuncDecl

	// initAssigns is a map of assignment expression -> InitN function call
	// statement.
	//
	// That InitN should contain the assignment statement for the
	// appropriate assignment expression.
	//
	// types.Info.InitOrder keeps track of Initializations by Lhs name and
	// Rhs ast.Expr.  We reparent the Rhs in assignment statements in InitN
	// functions, so we use the Rhs as an easy key here.
	// types.Info.InitOrder + initAssigns can then easily be used to derive
	// the order of Stmts in the "real" init.
	//
	// The key Expr must also be the AssignStmt.Rhs[0].
	initAssigns map[ast.Expr]ast.Stmt
}

// modules returns a list of module directories => directories of packages
// inside that module as well as packages that have no discernible module.
//
// The module for a package is determined by the first parent directory that
// contains a go.mod.
func modules(filesystemPaths []string) (map[string][]string, []string) {
	// list of module directory => directories of packages it likely contains
	moduledPackages := make(map[string][]string)
	var noModulePkgs []string
	for _, fullPath := range filesystemPaths {
		components := strings.Split(fullPath, "/")

		inModule := false
		for i := len(components); i >= 1; i-- {
			prefixPath := "/" + filepath.Join(components[:i]...)
			if _, err := os.Stat(filepath.Join(prefixPath, "go.mod")); err == nil {
				moduledPackages[prefixPath] = append(moduledPackages[prefixPath], fullPath)
				inModule = true
			}
		}
		if !inModule {
			noModulePkgs = append(noModulePkgs, fullPath)
		}
	}
	return moduledPackages, noModulePkgs
}

// We load file system paths differently, because there is a big difference between
//
//    go list -json ../../foobar
//
// and
//
//    (cd ../../foobar && go list -json .)
//
// Namely, PWD determines which go.mod to use. We want each
// package to use its own go.mod, if it has one.
func loadFSPackages(env golang.Environ, filesystemPaths []string) ([]*packages.Package, error) {
	var absPaths []string
	for _, fsPath := range filesystemPaths {
		absPath, err := filepath.Abs(fsPath)
		if err != nil {
			return nil, fmt.Errorf("could not find package at %q", fsPath)
		}
		absPaths = append(absPaths, absPath)
	}

	seen := make(map[string]struct{})
	var allps []*packages.Package

	addPkg := func(p *packages.Package) {
		if len(p.Errors) > 0 {
			// TODO(chrisko): should we return an error here instead of warn?
			log.Printf("Skipping package %v for errors: %v", p, p.Errors)
		} else if len(p.GoFiles) == 0 {
			log.Printf("Skipping package %v because it has no Go files", p)
		} else if p.Name != "main" {
			log.Printf("Skipping package %v because it is not a command (must be `package main`)", p)
		} else {
			dir := filepath.Dir(p.GoFiles[0])
			seen[dir] = struct{}{}
			allps = append(allps, p)
		}
	}

	mods, noModulePkgDirs := modules(absPaths)
	log.Printf("modules: %v", mods)

	for moduleDir, pkgDirs := range mods {
		pkgs, err := loadFSPkgs(env, moduleDir, pkgDirs...)
		if err != nil {
			return nil, fmt.Errorf("could not find packages %v in module %s: %v", pkgDirs, moduleDir, err)
		}
		for _, pkg := range pkgs {
			addPkg(pkg)
		}
	}

	if len(noModulePkgDirs) > 0 {
		// The directory we choose can be any dir that does not have a
		// go.mod anywhere in its parent tree.
		vendoredPkgs, err := loadFSPkgs(env, noModulePkgDirs[0], noModulePkgDirs...)
		if err != nil {
			return nil, fmt.Errorf("could not find packages %v: %v", noModulePkgDirs, err)
		}
		for _, p := range vendoredPkgs {
			addPkg(p)
		}
	}
	return allps, nil
}

// NewPackages collects package metadata about all named packages.
//
// names can either be directory paths or Go import paths.
func NewPackages(env golang.Environ, names ...string) ([]*Package, error) {
	var goImportPaths []string
	var filesystemPaths []string

	for _, name := range names {
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "/") {
			filesystemPaths = append(filesystemPaths, name)
		} else if _, err := os.Stat(name); err == nil {
			filesystemPaths = append(filesystemPaths, name)
		} else {
			goImportPaths = append(goImportPaths, name)
		}
	}

	var ps []*packages.Package
	if len(goImportPaths) > 0 {
		importPkgs, err := loadPkgs(env, "", goImportPaths...)
		if err != nil {
			return nil, fmt.Errorf("failed to load package %v: %v", goImportPaths, err)
		}
		for _, p := range importPkgs {
			if p.Name == "main" {
				ps = append(ps, p)
			} else {
				log.Printf("Skipping package %v because it is not a command (must be `package main`)", p)
			}
		}
	}

	pkgs, err := loadFSPackages(env, filesystemPaths)
	if err != nil {
		return nil, fmt.Errorf("could not load packages from file system: %v", err)
	}
	ps = append(ps, pkgs...)

	var ips []*Package
	for _, p := range ps {
		log.Printf("package: %s", p)
		ips = append(ips, NewPackage(path.Base(p.PkgPath), p))
	}
	return ips, nil
}

// loadFSPkgs looks up importDirs packages, making the import path relative to
// `dir`. `go list -json` requires the import path to be relative to the dir
// when the package is outside of a $GOPATH and there is no go.mod in any parent directory.
func loadFSPkgs(env golang.Environ, dir string, importDirs ...string) ([]*packages.Package, error) {
	var relImportDirs []string
	for _, importDir := range importDirs {
		relImportDir, err := filepath.Rel(dir, importDir)
		if err != nil {
			return nil, fmt.Errorf("Go package path %s is not relative to %s: %v", importDir, dir, err)
		}

		// N.B. `go list -json cmd/foo` is not the same as `go list -json ./cmd/foo`.
		//
		// The former looks for cmd/foo in $GOROOT or $GOPATH, while
		// the latter looks in the relative directory ./cmd/foo.
		relImportDirs = append(relImportDirs, "./"+relImportDir)
	}
	return loadPkgs(env, dir, relImportDirs...)
}

func loadPkgs(env golang.Environ, dir string, patterns ...string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedImports | packages.NeedFiles | packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedCompiledGoFiles | packages.NeedModule,
		Env:  append(os.Environ(), env.Env()...),
		Dir:  dir,
	}
	return packages.Load(cfg, patterns...)
}

// NewPackage creates a new Package based on an existing packages.Package.
func NewPackage(name string, p *packages.Package) *Package {
	pp := &Package{
		// Name is the executable name.
		Name:        path.Base(name),
		Pkg:         p,
		initAssigns: make(map[ast.Expr]ast.Stmt),
	}

	// This Init will hold calls to all other InitXs.
	pp.init = &ast.FuncDecl{
		Name: ast.NewIdent("Init"),
		Type: &ast.FuncType{
			Params:  &ast.FieldList{},
			Results: nil,
		},
		Body: &ast.BlockStmt{},
	}
	return pp
}

func (p *Package) nextInit(addToCallList bool) *ast.Ident {
	i := ast.NewIdent(fmt.Sprintf("Init%d", p.initCount))
	if addToCallList {
		p.init.Body.List = append(p.init.Body.List, &ast.ExprStmt{X: &ast.CallExpr{Fun: i}})
	}
	p.initCount++
	return i
}

// TODO:
// - write an init name generator, in case InitN is already taken.
func (p *Package) rewriteFile(f *ast.File) bool {
	hasMain := false

	// Change the package name declaration from main to the command's name.
	f.Name.Name = p.Name

	// Map of fully qualified package name -> imported alias in the file.
	importAliases := make(map[string]string)
	for _, impt := range f.Imports {
		if impt.Name != nil {
			importPath, err := strconv.Unquote(impt.Path.Value)
			if err != nil {
				panic(err)
			}
			importAliases[importPath] = impt.Name.Name
		}
	}

	// When the types.TypeString function translates package names, it uses
	// this function to map fully qualified package paths to a local alias,
	// if it exists.
	qualifier := func(pkg *types.Package) string {
		name, ok := importAliases[pkg.Path()]
		if ok {
			return name
		}
		// When referring to self, don't use any package name.
		if pkg == p.Pkg.Types {
			return ""
		}
		return pkg.Name()
	}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			// We only care about vars.
			if d.Tok != token.VAR {
				break
			}
			for _, spec := range d.Specs {
				s := spec.(*ast.ValueSpec)
				if s.Values == nil {
					continue
				}

				// For each assignment, create a new init
				// function, and place it in the same file.
				for i, name := range s.Names {
					varInit := &ast.FuncDecl{
						Name: p.nextInit(false),
						Type: &ast.FuncType{
							Params:  &ast.FieldList{},
							Results: nil,
						},
						Body: &ast.BlockStmt{
							List: []ast.Stmt{
								&ast.AssignStmt{
									Lhs: []ast.Expr{name},
									Tok: token.ASSIGN,
									Rhs: []ast.Expr{s.Values[i]},
								},
							},
						},
					}
					// Add a call to the new init func to
					// this map, so they can be added to
					// Init0() in the correct init order
					// later.
					p.initAssigns[s.Values[i]] = &ast.ExprStmt{X: &ast.CallExpr{Fun: varInit.Name}}
					f.Decls = append(f.Decls, varInit)
				}

				// Add the type of the expression to the global
				// declaration instead.
				if s.Type == nil {
					typ := p.Pkg.TypesInfo.Types[s.Values[0]]
					s.Type = ast.NewIdent(types.TypeString(typ.Type, qualifier))
				}
				s.Values = nil
			}

		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.Name == "main" {
				d.Name.Name = "Main"
				hasMain = true
			}
			if d.Recv == nil && d.Name.Name == "init" {
				d.Name = p.nextInit(true)
			}
		}
	}

	// Now we change any import names attached to package declarations. We
	// just upcase it for now; it makes it easy to look in bbsh for things
	// we changed, e.g. grep -r bbsh Import is useful.
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "// import") {
				c.Text = "// Import" + c.Text[9:]
			}
		}
	}
	return hasMain
}

// write writes p into destDir.
func writePkg(p *packages.Package, destDir string) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	log.Printf("package: %p %v %v", p, p.OtherFiles, p)
	for _, fp := range p.OtherFiles {
		log.Printf("fp: %v", fp)
		if err := cp.Copy(fp, filepath.Join(destDir, filepath.Base(fp))); err != nil {
			return fmt.Errorf("copy failed: %v", err)
		}
	}

	return writeFiles(destDir, p.Fset, p.Syntax)
}

func writeFiles(destDir string, fset *token.FileSet, files []*ast.File) error {
	// Write all files out.
	for _, file := range files {
		name := fset.File(file.Package).Name()

		path := filepath.Join(destDir, filepath.Base(name))
		if err := writeFile(path, fset, file); err != nil {
			return err
		}
	}
	return nil
}

// Rewrite rewrites p into destDir as a bb package, creating an Init and Main function.
func (p *Package) Rewrite(destDir string) error {
	// This init holds all variable initializations.
	//
	// func Init0() {}
	varInit := &ast.FuncDecl{
		Name: p.nextInit(true),
		Type: &ast.FuncType{
			Params:  &ast.FieldList{},
			Results: nil,
		},
		Body: &ast.BlockStmt{},
	}

	var mainFile *ast.File
	for _, sourceFile := range p.Pkg.Syntax {
		if hasMainFile := p.rewriteFile(sourceFile); hasMainFile {
			mainFile = sourceFile
		}
	}
	if mainFile == nil {
		return fmt.Errorf("no main function found in package %q", p.Pkg.PkgPath)
	}

	// Add variable initializations to Init0 in the right order.
	for _, initStmt := range p.Pkg.TypesInfo.InitOrder {
		a, ok := p.initAssigns[initStmt.Rhs]
		if !ok {
			return fmt.Errorf("couldn't find init assignment %s", initStmt)
		}
		varInit.Body.List = append(varInit.Body.List, a)
	}

	mainFile.Decls = append(mainFile.Decls, varInit, p.init)

	return writePkg(p.Pkg, destDir)
}

func writeFile(path string, fset *token.FileSet, f *ast.File) error {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return fmt.Errorf("error formatting Go file %q: %v", path, err)
	}
	return writeGoFile(path, buf.Bytes())
}

func writeGoFile(path string, code []byte) error {
	// Format the file. Do not fix up imports, as we only moved code around
	// within files.
	opts := imports.Options{
		Comments:   true,
		TabIndent:  true,
		TabWidth:   8,
		FormatOnly: true,
	}
	code, err := imports.Process("commandline", code, &opts)
	if err != nil {
		return fmt.Errorf("bad parse while processing imports %q: %v", path, err)
	}

	if err := ioutil.WriteFile(path, code, 0644); err != nil {
		return fmt.Errorf("error writing Go file to %q: %v", path, err)
	}
	return nil
}
