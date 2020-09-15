// Copyright 2015-2019 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package bb builds one busybox-like binary out of many Go command sources.
//
// This allows you to take two Go commands, such as Go implementations of `sl`
// and `cowsay` and compile them into one binary, callable like `./bb sl` and
// `./bb cowsay`. Which command is invoked is determined by `argv[0]` or
// `argv[1]` if `argv[0]` is not recognized.
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
// callable package functions. E.g. `main` becomes `registeredMain`, each
// `init` becomes `initN`, and global variable assignments are moved into their
// own `initN`.
package bb

import (
	"fmt"
	"go/ast"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/goterm/term"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"

	"github.com/u-root/gobusybox/src/pkg/bb/bbinternal"
	"github.com/u-root/gobusybox/src/pkg/findpkg"
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

// BuildBusybox builds a busybox of the given Go commands.
//
// cmdPaths is a list of Go import paths or file system directories containing
// Go commands. binaryPath is the output binary path. noStrip determines
// whether to strip all symbols from the busybox binary to save more space.
//
// env is the Go environment (GOOS, GOARCH, etc).
func BuildBusybox(env golang.Environ, cmdPaths []string, noStrip bool, binaryPath string) (nerr error) {
	tmpDir, err := ioutil.TempDir("", "bb-")
	if err != nil {
		return err
	}
	defer func() {
		if nerr != nil {
			log.Printf("Preserving bb generated source directory at %s due to error", tmpDir)
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
	cmds, err := bbinternal.NewPackages(env, cmdPaths...)
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

	bbFset, bbFiles, _, err := bbinternal.ParseAST("main", []string{filepath.Join(bbDir, "main.go")})
	if err != nil {
		return err
	}
	if len(bbFiles) == 0 {
		return fmt.Errorf("bb package not found")
	}

	// Fix the import path for bbmain, since we wrote bbmain/register.go into bbDir above.
	if !astutil.RewriteImport(bbFset, bbFiles[0], "github.com/u-root/gobusybox/src/pkg/bb/bbmain", "bb.u-root.com/bb/pkg/bbmain") {
		return fmt.Errorf("could not rewrite import")
	}

	// Create bb main.go.
	if err := bbinternal.CreateBBMainSource(bbFset, bbFiles, bbImports, bbDir); err != nil {
		return fmt.Errorf("creating bb main.go file failed: %v", err)
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
func localModules(pkgDir string, mainPkgs []*bbinternal.Package) ([]string, error) {
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
func dealWithDeps(env golang.Environ, bbDir, tmpDir, pkgDir string, mainPkgs []*bbinternal.Package) (bool, error) {
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

	// Copy local dependency packages into module directories at
	// tmpDir/src.
	seenIDs := make(map[string]struct{})
	for _, p := range localDepPkgs {
		if _, ok := seenIDs[p.ID]; !ok {
			if err := findpkg.WritePkg(p, filepath.Join(pkgDir, p.PkgPath)); err != nil {
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
			// Replaced modules can be local on the file system.
			if pkg.Module != nil && pkg.Module.Replace != nil && isReplacedModuleLocal(pkg.Module.Replace) {
				return true
			}

			// Is this a dependency within a local module?
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
