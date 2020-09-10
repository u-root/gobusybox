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
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/google/goterm/term"
	"golang.org/x/sys/unix"
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

// importPath returns the unquoted import path of s,
// or "" if the path is not properly quoted.
func importPath(s *ast.ImportSpec) string {
	t, err := strconv.Unquote(s.Path.Value)
	if err != nil {
		return ""
	}
	return t
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
	//   tmp/src/bb.u-root.com/bb
	//   tmp/src/bb.u-root.com/bb/main.go
	//      import "<module1>/cmd/foo"
	//      import "<module2>/cmd/bar"
	//
	//      func main()
	//
	// The only way to refer to other Go modules locally is to replace them
	// with local paths in a top-level go.mod:
	//
	//   tmp/src/bb.u-root.com/bb/go.mod
	//      package bb.u-root.com
	//
	//	replace <module1> => ../../<module1>
	//	replace <module2> => ../../<module2>
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

	bbDir := filepath.Join(tmpDir, "src/bb.u-root.com/bb")
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

	// List of packages to import in the real main file.
	var bbImports []string
	// Rewrite commands to packages.
	for _, cmd := range cmds {
		destination := filepath.Join(pkgDir, cmd.Pkg.PkgPath)

		if err := cmd.Rewrite(destination, "bb.u-root.com/bb/pkg/bbmain"); err != nil {
			return fmt.Errorf("rewriting command %q failed: %v", cmd.Pkg.PkgPath, err)
		}
		bbImports = append(bbImports, cmd.Pkg.PkgPath)
	}

	// Collect and write dependencies into pkgDir.
	hasModules, err := dealWithDeps(env, bbDir, tmpDir, pkgDir, cmds)
	if err != nil {
		return fmt.Errorf("dealing with deps: %v", err)
	}

	if err := writeBBMain(bbDir, tmpDir, bbImports); err != nil {
		return err
	}

	// Compile bb.
	if env.GO111MODULE == "off" || !hasModules {
		env.GOPATH = tmpDir
	}
	if err := env.BuildDir(bbDir, binaryPath, golang.BuildOpts{NoStrip: noStrip}); err != nil {
		return fmt.Errorf("go build: %v", err)
	}
	return nil
}

// writeBBMain writes $TMPDIR/src/bb.u-root.com/bb/pkg/bbmain/register.go and
// $TMPDIR/src/bb.u-root.com/bb/main.go.
//
// They are taken from ./bbmain/register.go and ./bbmain/cmd/main.go, but they
// do not retain their original import paths because the main command must be
// in a module that doesn't conflict with any bb commands. If one were to
// compile github.com/u-root/gobusybox/src/cmd/* into a busybox, we'd have
// problems -- the src/go.mod would conflict with our generated go.mod, and
// it'd be complicated to merge them. So they are transplanted into the
// bb.u-root.com/bb module.
func writeBBMain(bbDir, tmpDir string, bbImports []string) error {
	if err := os.MkdirAll(filepath.Join(bbDir, "pkg/bbmain"), 0755); err != nil {
		return err
	}
	if err := ioutil.WriteFile(filepath.Join(bbDir, "pkg/bbmain/register.go"), bbRegisterSource, 0755); err != nil {
		return err
	}
	if err := ioutil.WriteFile(filepath.Join(bbDir, "main.go"), bbMainSource, 0755); err != nil {
		return err
	}

	bbFset, bbFiles, _, err := ParseAST("main", []string{filepath.Join(bbDir, "main.go")})
	if err != nil {
		return err
	}
	if len(bbFiles) == 0 {
		return fmt.Errorf("bb package not found")
	}

	if !astutil.RewriteImport(bbFset, bbFiles[0], "github.com/u-root/gobusybox/src/pkg/bb/bbmain", "bb.u-root.com/bb/pkg/bbmain") {
		return fmt.Errorf("could not rewrite import")
	}

	// Create bb main.go.
	if err := CreateBBMainSource(bbFset, bbFiles, bbImports, bbDir); err != nil {
		return fmt.Errorf("creating bb main() file failed: %v", err)
	}
	return nil
}

func isReplacedModuleLocal(m *packages.Module) bool {
	// From "replace directive": https://golang.org/ref/mod#go
	//
	//   If the path on the right side of the arrow is an absolute or
	//   relative path (beginning with ./ or ../), it is interpreted as the
	//   local file path to the replacement module root directory, which
	//   must contain a go.mod file. The replacement version must be
	//   omitted in this case.
	return strings.HasPrefix(m.Path, "./") || strings.HasPrefix(m.Path, "../") || strings.HasPrefix(m.Path, "/")
}

// localModules finds all modules that are local, copies their go.mod in the
// right place, and raises an error if any modules have conflicting replace
// directives.
func localModules(pkgDir string, mainPkgs []*Package) ([]string, error) {
	copyGoMod := func(mod *packages.Module) error {
		if mod == nil {
			return nil
		}

		if err := os.MkdirAll(filepath.Join(pkgDir, mod.Path), 0755); os.IsExist(err) {
			return nil
		} else if err != nil {
			return err
		}

		// Use the module file for all outside dependencies.
		return cp.Copy(mod.GoMod, filepath.Join(pkgDir, mod.Path, "go.mod"))
	}

	type localModule struct {
		m          *packages.Module
		provenance string
	}
	localModules := make(map[string]*localModule)
	// Find all top-level modules.
	for _, p := range mainPkgs {
		if p.Pkg.Module != nil {
			if _, ok := localModules[p.Pkg.Module.Path]; !ok {
				localModules[p.Pkg.Module.Path] = &localModule{
					m:          p.Pkg.Module,
					provenance: fmt.Sprintf("your request to compile %s from %s", p.Pkg.Module.Path, p.Pkg.Module.Dir),
				}

				if err := copyGoMod(p.Pkg.Module); err != nil {
					return nil, fmt.Errorf("failed to copy go.mod for %s: %v", p.Pkg.Module.Path, err)
				}
			}
		}
	}

	for _, p := range mainPkgs {
		replacedModules := locallyReplacedModules(p.Pkg)
		for modPath, module := range replacedModules {
			if original, ok := localModules[modPath]; ok {
				// Is this module different from one that a
				// previous definition provided?
				//
				// This only looks for 2 conflicting *local* module definitions.
				if original.m.Dir != module.Dir {
					return nil, fmt.Errorf("two conflicting versions of module %s have been requested; one from %s, the other from %s's go.mod",
						modPath, original.provenance, p.Pkg.Module.Path)
				}
			} else {
				localModules[modPath] = &localModule{
					m:          module,
					provenance: fmt.Sprintf("%s's go.mod (%s)", p.Pkg.Module.Path, p.Pkg.Module.GoMod),
				}

				if err := copyGoMod(module); err != nil {
					return nil, fmt.Errorf("failed to copy go.mod for %s: %v", p.Pkg.Module.Path, err)
				}
			}
		}
	}

	// Look for conflicts between remote and local modules.
	//
	// E.g. if u-bmc depends on u-root, but we are also compiling u-root locally.
	var conflict bool
	for _, mainPkg := range mainPkgs {
		packages.Visit([]*packages.Package{mainPkg.Pkg}, nil, func(p *packages.Package) {
			if p.Module == nil {
				return
			}
			if l, ok := localModules[p.Module.Path]; ok && l.m.Dir != p.Module.Dir {
				fmt.Fprintln(os.Stderr, "")
				log.Printf("Conflicting module dependencies on %s:", p.Module.Path)
				log.Printf("  %s uses %s", mainPkg.Pkg.Module.Path, moduleIdentifier(p.Module))
				log.Printf("  %s uses %s", l.provenance, moduleIdentifier(l.m))
				replacePath, err := filepath.Rel(mainPkg.Pkg.Module.Dir, l.m.Dir)
				if err != nil {
					replacePath = l.m.Dir
				}
				fmt.Fprintln(os.Stderr, "")
				log.Printf("%s: add `replace %s => %s` to %s", term.Bold("Suggestion to resolve"), p.Module.Path, replacePath, mainPkg.Pkg.Module.GoMod)
				fmt.Fprintln(os.Stderr, "")
				conflict = true
			}
		})
	}
	if conflict {
		return nil, fmt.Errorf("conflicting module dependencies found")
	}

	var modules []string
	for modPath := range localModules {
		modules = append(modules, modPath)
	}
	return modules, nil
}

func moduleIdentifier(m *packages.Module) string {
	if m.Replace != nil && isReplacedModuleLocal(m.Replace) {
		return fmt.Sprintf("directory %s", m.Replace.Path)
	}
	return fmt.Sprintf("version %s", m.Version)
}

// dealWithDeps tries to suss out local files that need to be in the tree.
//
// It helps to have read https://golang.org/ref/mod when editing this function.
func dealWithDeps(env golang.Environ, bbDir, tmpDir, pkgDir string, mainPkgs []*Package) (bool, error) {
	// Module-enabled Go programs resolve their dependencies in one of two ways:
	//
	// - locally, if the dependency is *in* the module or there is a local replace directive
	// - remotely, if not local
	//
	// I.e. if the module is github.com/u-root/u-root,
	//
	// - local: github.com/u-root/u-root/pkg/uio
	// - remote: github.com/hugelgupf/p9/p9
	// - also local: a remote module, with a local replace rule
	//
	// For local dependencies, we copy all dependency packages' files over,
	// as well as their go.mod files.
	//
	// Remote dependencies are expected to be resolved from main packages'
	// go.mod and local dependencies' go.mod files, which all must be in
	// the tree.
	localModules, err := localModules(pkgDir, mainPkgs)
	if err != nil {
		return false, err
	}

	var localDepPkgs []*packages.Package
	for _, p := range mainPkgs {
		// Find all dependency packages that are *within* module boundaries for this package.
		//
		// writeDeps also copies the go.mod into the right place.
		localDeps := collectDeps(p.Pkg, localModules)
		localDepPkgs = append(localDepPkgs, localDeps...)
	}

	// TODO(chrisko): We need to go through mainPkgs Module definitions to
	// find exclude and replace directives, which only have an effect in
	// the main module's go.mod, which will be the top-level go.mod we
	// write.
	//
	// mainPkgs module files expect to be "the main module", since those
	// are where Go compilation would normally occur.
	//
	// The top-level go.mod must have copies of the mainPkgs' modules'
	// replace and exclude directives. If they conflict, we need to have a
	// legible error message for the user.

	// Copy go.mod files for all local modules (= command modules, and
	// local dependency modules they depend on).
	/*for _, p := range localDepPkgs {
		// Only replaced modules can be potentially local.
		if p.Module != nil && p.Module.Replace != nil && isReplacedModuleLocal(p.Module.Replace) {

			// All local modules must be declared in the top-level go.mod
			modules[p.Module.Path] = struct{}{}
		}
	}

	for _, m := range localModules {
		if p.Pkg.Module != nil {
			modules[p.Pkg.Module.Path] = struct{}{}
		}

		if err := copyGoMod(p.Pkg.Module); err != nil {
			return false, fmt.Errorf("failed to copy go.mod for %s: %v", p.Pkg, err)
		}
	}*/

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
	if env.GO111MODULE == "on" || len(localModules) > 0 {
		// go.mod for the bb binary.
		//
		// Add local replace rules for all modules we're compiling.
		//
		// This is the only way to locally reference another modules'
		// repository. Otherwise, go'll try to go online to get the source.
		//
		// The module name is something that'll never be online, lest Go
		// decides to go on the internet.
		content := `module bb.u-root.com/bb`
		for _, mpath := range localModules {
			content += fmt.Sprintf("\nreplace %s => ../../%s\n", mpath, mpath)
		}

		// TODO(chrisko): add other go.mod files' replace and exclude
		// directives.
		//
		// Warn the user if they are potentially incompatible.
		if err := ioutil.WriteFile(filepath.Join(bbDir, "go.mod"), []byte(content), 0755); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// deps recursively iterates through imports and returns the set of packages
// for which filter returns true.
func deps(p *packages.Package, filter func(p *packages.Package) bool) []*packages.Package {
	var pkgs []*packages.Package
	packages.Visit([]*packages.Package{p}, nil, func(pkg *packages.Package) {
		if filter(pkg) {
			pkgs = append(pkgs, pkg)
		}
	})
	return pkgs
}

func locallyReplacedModules(p *packages.Package) map[string]*packages.Module {
	if p.Module == nil {
		return nil
	}

	m := make(map[string]*packages.Module)
	// Collect all "local" dependency packages that are in `replace`
	// directives somewhere, to be copied into the temporary directory
	// structure later.
	packages.Visit([]*packages.Package{p}, nil, func(pkg *packages.Package) {
		if pkg.Module != nil && pkg.Module.Replace != nil && isReplacedModuleLocal(pkg.Module.Replace) {
			m[pkg.Module.Path] = pkg.Module
		}
	})
	return m
}

func collectDeps(p *packages.Package, localModules []string) []*packages.Package {
	if p.Module != nil {
		// Collect all "local" dependency packages, to be copied into
		// the temporary directory structure later.
		return deps(p, func(pkg *packages.Package) bool {
			if pkg.Module != nil && pkg.Module.Replace != nil && isReplacedModuleLocal(pkg.Module.Replace) {
				return true
			}

			// Is this a dependency within the module?
			for _, modulePath := range localModules {
				if strings.HasPrefix(pkg.PkgPath, modulePath) {
					return true
				}
			}
			return false
		})
	}

	// If modules are not enabled, we need a copy of *ALL*
	// non-standard-library dependencies in the temporary directory.
	return deps(p, func(pkg *packages.Package) bool {
		// First component of package path contains a "."?
		//
		// Poor man's standard library test.
		firstComp := strings.SplitN(pkg.PkgPath, "/", 2)
		return strings.Contains(firstComp[0], ".")
	})
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

// CreateBBMainSource creates a bb Go command that imports all given pkgs.
//
// p must be the bb template.
//
// - For each pkg in pkgs, add
//     import _ "pkg"
//   to astp's first file.
// - Write source file out to destDir.
func CreateBBMainSource(fset *token.FileSet, files []*ast.File, pkgs []string, destDir string) error {
	if len(files) != 1 {
		return fmt.Errorf("bb cmd template is supposed to only have one file")
	}

	for _, pkg := range pkgs {
		astutil.AddNamedImport(fset, files[0], "_", pkg)
	}
	return writeFiles(destDir, fset, files)
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

	// mainFuncName is the name for the renamed main().
	mainFuncName string

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
// The module for a package is determined by the **first** parent directory
// that contains a go.mod.
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
				break
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

	var allps []*packages.Package

	// Find each packages' module, and batch package queries together by module.
	//
	// Query all packages that don't have a module at all together, as well.
	//
	// Batching these queries saves a *lot* of time; on the order of
	// several minutes for 30+ commands.
	mods, noModulePkgDirs := modules(absPaths)

	for moduleDir, pkgDirs := range mods {
		pkgs, err := loadFSPkgs(env, moduleDir, pkgDirs...)
		if err != nil {
			return nil, fmt.Errorf("could not find packages in module %s: %v", moduleDir, err)
		}
		for _, pkg := range pkgs {
			allps = addPkg(allps, pkg)
		}
	}

	if len(noModulePkgDirs) > 0 {
		// The directory we choose can be any dir that does not have a
		// go.mod anywhere in its parent tree.
		vendoredPkgs, err := loadFSPkgs(env, noModulePkgDirs[0], noModulePkgDirs...)
		if err != nil {
			return nil, fmt.Errorf("could not find packages: %v", err)
		}
		for _, p := range vendoredPkgs {
			allps = addPkg(allps, p)
		}
	}
	return allps, nil
}

func addPkg(plist []*packages.Package, p *packages.Package) []*packages.Package {
	if len(p.Errors) > 0 {
		// TODO(chrisko): should we return an error here instead of warn?
		log.Printf("Skipping package %v for errors:", p)
		packages.PrintErrors([]*packages.Package{p})
	} else if len(p.GoFiles) == 0 {
		log.Printf("Skipping package %v because it has no Go files", p)
	} else if p.Name != "main" {
		log.Printf("Skipping package %v because it is not a command (must be `package main`)", p)
	} else {
		plist = append(plist, p)
	}
	return plist
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
			ps = addPkg(ps, p)
		}
	}

	pkgs, err := loadFSPackages(env, filesystemPaths)
	if err != nil {
		return nil, fmt.Errorf("could not load packages from file system: %v", err)
	}
	ps = append(ps, pkgs...)

	var ips []*Package
	for _, p := range ps {
		ips = append(ips, NewPackage(path.Base(p.PkgPath), p))
	}
	return ips, nil
}

// loadFSPkgs looks up importDirs packages, making the import path relative to
// `dir`. `go list -json` requires the import path to be relative to the dir
// when the package is outside of a $GOPATH and there is no go.mod in any parent directory.
func loadFSPkgs(env golang.Environ, dir string, importDirs ...string) ([]*packages.Package, error) {
	// Eligibility check: does each directory contain files that are
	// compilable under the current GOROOT/GOPATH/GOOS/GOARCH and build
	// tags?
	//
	// In Go 1.14 and Go 1.15, this is done by packages.Load on a
	// per-package basis, which is why batching queries works out well. In
	// Go 1.13, the entire query fails with no indication of which package
	// made it fail, so we need to filter out commands that do not have
	// compilable files first.
	var compilableImportDirs []string
	for _, importDir := range importDirs {
		f, err := os.Open(importDir)
		if err != nil {
			return nil, err
		}
		names, err := f.Readdirnames(0)
		if errors.Is(err, unix.ENOTDIR) {
			return nil, fmt.Errorf("Go busybox requires a list of directories; failed to read directory %s: %v", importDir, err)
		} else if err != nil {
			return nil, fmt.Errorf("could not determine file names for %s: %v", importDir, err)
		}
		foundOne := false
		for _, name := range names {
			if match, err := env.Context.MatchFile(importDir, name); err != nil {
				// This pretty much only returns an error if
				// the file cannot be opened or read.
				return nil, fmt.Errorf("could not determine Go build constraints of %s: %v", importDir, err)
			} else if match {
				foundOne = true
				break
			}
		}
		if foundOne {
			compilableImportDirs = append(compilableImportDirs, importDir)
		} else {
			log.Printf("Skipping directory %s because build constraints exclude all Go files", importDir)
		}
	}

	if len(compilableImportDirs) == 0 {
		return nil, fmt.Errorf("build constraints excluded all requested commands")
	}

	// Make all paths relative, because packages.Load/`go list -json` does
	// not like absolute paths sometimes.
	var relImportDirs []string
	for _, importDir := range compilableImportDirs {
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

	pp.mainFuncName = pp.newFunctionName("registeredMain")

	// This Init will hold calls to all other InitXs.
	pp.init = &ast.FuncDecl{
		Name: ast.NewIdent(pp.newFunctionName("registeredInit")),
		Type: &ast.FuncType{
			Params:  &ast.FieldList{},
			Results: nil,
		},
		Body: &ast.BlockStmt{},
	}
	return pp
}

func (p *Package) nextInit(addToCallList bool) *ast.Ident {
	nextInitName := fmt.Sprintf("busyboxInit%d", p.initCount)
	for p.funcNameTaken(nextInitName) {
		p.initCount++
		nextInitName = fmt.Sprintf("busyboxInit%d", p.initCount)
	}
	i := ast.NewIdent(nextInitName)
	if addToCallList {
		p.init.Body.List = append(p.init.Body.List, &ast.ExprStmt{X: &ast.CallExpr{Fun: i}})
	}
	p.initCount++
	return i
}

// importName finds the package path to import, given the go/types pkg path.
//
// E.g. go/types uses the fully vendored name of a package, such as
// github.com/u-root/u-root/vendor/golang.org/x/sys/unix. importName returns
// the name that should appear in the import statement for this package, which
// is golang.org/x/sys/unix.
//
// Since the only way this happens is through an implicit import, we know that
// somewhere in the dependency tree this package must exist, so we visit all
// dependencies looking for the go/types package path looking for a valid
// package import path statement.
func importName(p *packages.Package, typePkgPath string) string {
	var importName string
	// Go through all dependent packages.
	packages.Visit([]*packages.Package{p}, func(p *packages.Package) bool {
		// Yeah, packages.Visit already goes through all imports -- but
		// it does not give us the index of the p.Imports map, which is
		// the "import paths appearing in the package's Go source
		// files".
		for name, pkg := range p.Imports {
			if pkg.PkgPath == typePkgPath {
				importName = name
				return false
			}
		}
		return true
	}, nil)
	if len(importName) > 0 {
		return importName
	}
	// It doesn't appear. We'll go import it.
	return typePkgPath
}

// pkgImportNameTaken checks whether name would conflict with any existing
// imports in f or variable/const/func declarations in p.
//
// Import statements may conflict with import statements in other files in
// the same package.
func (p *Package) pkgImportNameTaken(name string, f *ast.File) bool {
	// package scope is all variable, const, and func names
	if p.Pkg.Types.Scope().Lookup(name) != nil {
		return true
	}

	// file scope is imports. Only imports from this file can conflict.
	// Imports in other files have no effect.
	if p.Pkg.TypesInfo.Scopes[f].Lookup(name) != nil {
		return true
	}
	return false
}

// funcNameTaken checks whether name would conflict with any
// import/variable/const/func declarations in all of p.
//
// Variable/const/func names may not conflict with import statements in
// other files of the same package!
func (p *Package) funcNameTaken(name string) bool {
	// package scope is all variable, const, and func names
	if p.Pkg.Types.Scope().Lookup(name) != nil {
		return true
	}

	// file scope is all imports
	for _, file := range p.Pkg.Syntax {
		if p.Pkg.TypesInfo.Scopes[file].Lookup(name) != nil {
			return true
		}
	}
	return false
}

// newFunctionName returns an unused function name in p with the prefix name.
func (p *Package) newFunctionName(name string) string {
	var i int
	proposed := name
	for p.funcNameTaken(proposed) {
		proposed = fmt.Sprintf("%s%d", name, i)
		i++
	}
	return proposed
}

// newImportName returns an unused import name in f/p with the prefix name.
func (p *Package) newImportName(name string, f *ast.File) string {
	var i int
	proposed := name
	for p.pkgImportNameTaken(proposed, f) {
		proposed = fmt.Sprintf("%s%d", name, i)
		i++
	}
	return proposed
}

func (p *Package) rewriteFile(f *ast.File) bool {
	hasMain := false

	// Change the package name declaration from main to the command's name.
	f.Name.Name = p.Name

	// Map of fully qualified package name -> imported alias in the file.
	importAliases := make(map[string]string)
	unaliasedImports := make(map[string]struct{})
	for _, impt := range f.Imports {
		importPath, err := strconv.Unquote(impt.Path.Value)
		if err != nil {
			panic(err)
		}

		if impt.Name != nil {
			importAliases[importPath] = impt.Name.Name
		} else {
			// We do not record the name of the package, because we
			// do not know it. However, `qualifier` will know it
			// because it's passed in.
			unaliasedImports[importPath] = struct{}{}
		}
	}

	// When the types.TypeString function translates package names, it uses
	// this function to map fully qualified package paths to a local alias,
	// if it exists.
	qualifier := func(pkg *types.Package) string {
		// pkg.Path() = fully vendored package name.
		// importPath = package name as it appears in `import` statement.
		importPath := importName(p.Pkg, pkg.Path())

		if name, ok := importAliases[importPath]; ok {
			return name
		}
		if _, ok := unaliasedImports[importPath]; ok {
			// The package name is NOT the last component of its path.
			return pkg.Name()
		}
		// When referring to self, don't use any package name.
		if pkg == p.Pkg.Types {
			return ""
		}

		// This type is not imported in this file yet.
		//
		// This typically happens when a derived global import uses a
		// type that was previously only implicitly used.
		//
		// E.g. if we call func Foo() *log.Logger like this:
		//
		//   var l = pkg.Foo()
		//
		// Then it's possible the `log` package was not referred to at
		// all previously, and we now need to add an import for log.
		importAlias := p.newImportName(pkg.Name(), f)
		astutil.AddNamedImport(p.Pkg.Fset, f, importAlias, importPath)
		// Make sure we do not add this import twice.
		importAliases[importPath] = importAlias

		return importAlias
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
				d.Name.Name = p.mainFuncName
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
	//
	// TODO(chrisko): We don't have to do this anymore.
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

	for _, fp := range p.OtherFiles {
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
func (p *Package) Rewrite(destDir, bbImportPath string) error {
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

	// import bbmain "bbImportPath"
	importName := p.newImportName("bbmain", mainFile)
	astutil.AddNamedImport(p.Pkg.Fset, mainFile, importName, bbImportPath)

	// func init() {
	//   bbmain.Register("p.name", Init, Main)
	// }
	bbRegisterSelf := &ast.FuncDecl{
		Name: ast.NewIdent("init"),
		Type: &ast.FuncType{},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ExprStmt{X: &ast.CallExpr{
					Fun: ast.NewIdent(fmt.Sprintf("%s.Register", importName)),
					Args: []ast.Expr{
						// name=
						&ast.BasicLit{
							Kind:  token.STRING,
							Value: strconv.Quote(p.Name),
						},
						// init=
						ast.NewIdent(p.init.Name.Name),
						// main=
						ast.NewIdent(p.mainFuncName),
					},
				}},
			},
		},
	}

	mainFile.Decls = append(mainFile.Decls, varInit, p.init, bbRegisterSelf)

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
