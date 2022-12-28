// Copyright 2015-2019 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package findpkg finds packages from user-input strings that are either file
// paths or Go package paths.
package findpkg

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/go-multierror"
	"github.com/u-root/gobusybox/src/pkg/bb/bbinternal"
	"github.com/u-root/gobusybox/src/pkg/golang"
	"github.com/u-root/uio/ulog"
	"golang.org/x/tools/go/packages"
)

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

// Find each packages' module, and batch package queries together by module.
//
// Query all packages that don't have a module at all together, as well.
//
// Batching these queries saves a *lot* of time; on the order of
// several minutes for 30+ commands.
func batchFSPackages(l ulog.Logger, absPaths []string, loadFunc func(moduleDir string, dirs []string) error) error {
	mods, noModulePkgDirs := modules(absPaths)

	for moduleDir, pkgDirs := range mods {
		if err := loadFunc(moduleDir, pkgDirs); err != nil {
			return err
		}
	}

	if len(noModulePkgDirs) > 0 {
		if err := loadFunc(noModulePkgDirs[0], noModulePkgDirs); err != nil {
			return err
		}
	}
	return nil
}

// We look up file system paths differently, because there is a big difference between
//
//	go list -json ../../foobar
//
// and
//
//	(cd ../../foobar && go list -json .)
//
// Namely, PWD determines which go.mod to use. We want each
// package to use its own go.mod, if it has one.
//
// The easiest implementation would be to do (cd $packageDir && go list -json
// .), however doing that N times is very expensive -- takes several minutes
// for 30 packages. So here, we figure out every module involved and do one
// query per module and one query for everything that isn't in a module.
func batchLoadFSPackages(l ulog.Logger, env golang.Environ, absPaths []string) ([]*packages.Package, error) {
	var allps []*packages.Package

	err := batchFSPackages(l, absPaths, func(moduleDir string, packageDirs []string) error {
		pkgs, err := loadFSPkgs(l, env, moduleDir, packageDirs...)
		if err != nil {
			return fmt.Errorf("could not find packages in module %s: %v", moduleDir, err)
		}
		for _, pkg := range pkgs {
			allps, err = addPkg(l, allps, pkg)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return allps, nil
}

func addPkg(l ulog.Logger, plist []*packages.Package, p *packages.Package) ([]*packages.Package, error) {
	if len(p.Errors) > 0 {
		var merr error
		for _, e := range p.Errors {
			merr = multierror.Append(merr, e)
		}
		return plist, fmt.Errorf("failed to add package %v for errors: %v", p, merr)
	} else if len(p.GoFiles) == 0 {
		l.Printf("Skipping package %v because it has no Go files", p)
	} else if p.Name != "main" {
		l.Printf("Skipping package %v because it is not a command (must be `package main`)", p)
	} else {
		plist = append(plist, p)
	}
	return plist, nil
}

// NewPackages collects package metadata about all named packages.
//
// names can either be directory paths or Go import paths, with globs.
//
// It skips directories that do not have Go files subject to the build
// constraints in env and logs a "Skipping package {}" statement about such
// directories/packages.
//
// Allowed formats for names:
//
//   - relative and absolute paths including globs following Go's filepath.Match format.
//   - Go package paths; e.g. github.com/u-root/u-root/cmds/core/ls
//   - Globs of Go package paths, e.g github.com/u-root/u-root/cmds/i* (using path.Match format).
//   - Go package path expansions with ..., e.g. github.com/u-root/u-root/cmds/core/...
//
// If an entry starts with "-", it excludes the matching package(s).
//
// Examples:
//
//	./foobar
//	./foobar/glob*
//	github.com/u-root/u-root/cmds/core/...
//	github.com/u-root/u-root/cmds/core/ip
//	github.com/u-root/u-root/cmds/core/g*lob
//
// Globs of Go package paths must be within module boundaries to give accurate
// results, i.e. a glob that spans 2 Go modules may give unpredictable results.
func NewPackages(l ulog.Logger, env golang.Environ, workingDirectory string, names ...string) ([]*packages.Package, error) {
	var goImportPaths []string
	var filesystemPaths []string

	// Two steps:
	//
	// 1. Resolve globs, filter packages with build constraints.
	//    Produce an explicit list of packages.
	//
	// 2. Look up every piece of information necessary from those packages.
	//    (Includes optimizations to reduce the amount of time it takes to
	//    do type-checking, etc.)

	// Step 1.
	paths, err := ResolveGlobs(l, env, workingDirectory, names)
	if err != nil {
		return nil, err
	}

	// Step 2.
	for _, name := range paths {
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
		importPkgs, err := loadPkgs(env, workingDirectory, goImportPaths...)
		if err != nil {
			return nil, fmt.Errorf("failed to load package %v: %v", goImportPaths, err)
		}
		for _, p := range importPkgs {
			ps, err = addPkg(l, ps, p)
			if err != nil {
				return nil, err
			}
		}
	}

	pkgs, err := batchLoadFSPackages(l, env, filesystemPaths)
	if err != nil {
		return nil, fmt.Errorf("could not load packages from file system: %v", err)
	}
	ps = append(ps, pkgs...)
	return ps, nil
}

// NewBBPackages collects package metadata about all named packages. See
// NewPackages for documentation on the names argument.
func NewBBPackages(l ulog.Logger, env golang.Environ, names ...string) ([]*bbinternal.Package, error) {
	ps, err := NewPackages(l, env, "", names...)
	if err != nil {
		return nil, err
	}

	var ips []*bbinternal.Package
	for _, p := range ps {
		ips = append(ips, bbinternal.NewPackage(path.Base(p.PkgPath), p))
	}
	return ips, nil
}

// loadFSPkgs looks up importDirs packages, making the import path relative to
// `dir`. `go list -json` requires the import path to be relative to the dir
// when the package is outside of a $GOPATH and there is no go.mod in any parent directory.
func loadFSPkgs(l ulog.Logger, env golang.Environ, dir string, importDirs ...string) ([]*packages.Package, error) {
	// Make all paths relative, because packages.Load/`go list -json` does
	// not like absolute paths sometimes.
	//
	// N.B.(hugelgupf): I don't remember why this is here.
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
		Mode: packages.NeedName | packages.NeedImports | packages.NeedFiles | packages.NeedDeps | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedCompiledGoFiles | packages.NeedModule | packages.NeedEmbedFiles,
		Env:  append(os.Environ(), env.Env()...),
		Dir:  dir,
	}
	return packages.Load(cfg, patterns...)
}

func filterDirectoryPaths(l ulog.Logger, env golang.Environ, includes []string, excludes []string) ([]string, error) {
	var directories []string
	for _, match := range includes {
		// Skip anything that is not a directory, as only directories can be packages.
		fileInfo, _ := os.Stat(match)
		if !fileInfo.IsDir() {
			continue
		}
		absPath, _ := filepath.Abs(match)

		directories = append(directories, absPath)
	}

	// Exclusion doesn't have to go through the eligibility check.
	for i, e := range excludes {
		absPath, _ := filepath.Abs(e)
		excludes[i] = absPath
	}

	// Eligibility check: does each directory contain files that are
	// compilable under the current GOROOT/GOPATH/GOOS/GOARCH and build
	// tags?
	//
	// We filter this out first, because while packages.Load will give us
	// an error for this, it is not distinguishable from other errors. We
	// would like to give only a warning for these.
	//
	// This eligibility check requires Go 1.15, as before Go 1.15 the
	// package loader would return an error "cannot find package" for
	// packages not meeting build constraints.
	var allps []*packages.Package
	err := batchFSPackages(l, directories, func(moduleDir string, packageDirs []string) error {
		pkgs, err := lookupPkgNameAndFiles(env, moduleDir, packageDirs...)
		if err != nil {
			return fmt.Errorf("could not look up packages %q: %v", packageDirs, err)
		}
		allps = append(allps, pkgs...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	eligiblePkgs, err := checkEligibility(l, allps)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range eligiblePkgs {
		paths = append(paths, filepath.Dir(p.GoFiles[0]))
	}
	return excludePaths(paths, excludes), nil
}

func checkEligibility(l ulog.Logger, pkgs []*packages.Package) ([]*packages.Package, error) {
	var goodPkgs []*packages.Package
	var merr error
	for _, p := range pkgs {
		// If there's a build constraint issue, short out early and
		// neither add the package nor add an error -- just log a skip
		// note.
		if len(p.GoFiles) == 0 && len(p.IgnoredFiles) > 0 {
			l.Printf("Skipping package %s because build constraints exclude all Go files", p.PkgPath)
		} else if len(p.Errors) == 0 {
			goodPkgs = append(goodPkgs, p)
		} else {
			// We'll definitely return an error in the end, but
			// we're not returning early because we want to give
			// the user as much information as possible.
			for _, e := range p.Errors {
				merr = multierror.Append(merr, fmt.Errorf("package %s: %w", p.PkgPath, e))
			}
		}
	}
	if merr != nil {
		return nil, merr
	}
	return goodPkgs, nil
}

func excludePaths(paths []string, exclusions []string) []string {
	excludes := map[string]struct{}{}
	for _, p := range exclusions {
		excludes[p] = struct{}{}
	}

	var result []string
	for _, p := range paths {
		if _, ok := excludes[p]; !ok {
			result = append(result, p)
		}
	}
	return result
}

// Just looking up the stuff that doesn't take forever to parse.
func lookupPkgNameAndFiles(env golang.Environ, dir string, patterns ...string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles,
		Env:  append(os.Environ(), env.Env()...),
		Dir:  dir,
	}
	return packages.Load(cfg, patterns...)
}

func couldBeGlob(s string) bool {
	return strings.ContainsAny(s, "*?[") || strings.Contains(s, `\\`)
}

// lookupPkgsWithGlob resolves globs in Go package paths to a realized list of
// Go command paths. It may return a list that contains errors.
//
// Precondition: couldBeGlob(pattern) is true
func lookupPkgsWithGlob(env golang.Environ, wd string, pattern string) ([]*packages.Package, error) {
	elems := strings.Split(pattern, "/")

	globIndex := 0
	for i, e := range elems {
		if couldBeGlob(e) {
			globIndex = i
			break
		}
	}

	nonGlobPath := strings.Join(append(elems[:globIndex], "..."), "/")

	pkgs, err := lookupPkgNameAndFiles(env, wd, nonGlobPath)
	if err != nil {
		return nil, fmt.Errorf("%q is neither package or path/glob -- could not lookup %q (import path globs have to be within modules): %v", pattern, nonGlobPath, err)
	}

	// Apply the glob.
	var filteredPkgs []*packages.Package
	for _, p := range pkgs {
		if matched, err := path.Match(pattern, p.PkgPath); err != nil {
			return nil, fmt.Errorf("could not match %q to %q: %v", pattern, p.PkgPath, err)
		} else if matched {
			filteredPkgs = append(filteredPkgs, p)
		}
	}
	return filteredPkgs, nil
}

// lookupCompilablePkgsWithGlob resolves Go package path globs to a realized
// list of Go command paths. It filters out packages that have no files
// matching our build constraints and other errors.
func lookupCompilablePkgsWithGlob(l ulog.Logger, env golang.Environ, wd string, patterns ...string) ([]string, error) {
	var pkgs []*packages.Package
	// Batching saves time. Patterns with globs cannot be batched.
	//
	// When you batch requests you cannot attribute which result came from
	// which individual request. For globs, we need to be able to do
	// path.Match-ing on the results. So no batching of globs.
	var batchedPatterns []string
	for _, pattern := range patterns {
		if couldBeGlob(pattern) {
			ps, err := lookupPkgsWithGlob(env, wd, pattern)
			if err != nil {
				return nil, err
			}
			pkgs = append(pkgs, ps...)
		} else {
			batchedPatterns = append(batchedPatterns, pattern)
		}
	}
	if len(batchedPatterns) > 0 {
		ps, err := lookupPkgNameAndFiles(env, wd, batchedPatterns...)
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, ps...)
	}

	eligiblePkgs, err := checkEligibility(l, pkgs)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range eligiblePkgs {
		paths = append(paths, p.PkgPath)
	}
	return paths, nil
}

func filterGoPaths(l ulog.Logger, env golang.Environ, wd string, gopathIncludes, gopathExcludes []string) ([]string, error) {
	goInc, err := lookupCompilablePkgsWithGlob(l, env, wd, gopathIncludes...)
	if err != nil {
		return nil, err
	}

	goExc, err := lookupCompilablePkgsWithGlob(l, env, wd, gopathExcludes...)
	if err != nil {
		return nil, err
	}
	return excludePaths(goInc, goExc), nil
}

var errNoMatch = fmt.Errorf("no Go commands match the given patterns")

// ResolveGlobs takes a list of Go paths and directories that may
// include globs and returns a valid list of Go commands (either addressed by
// Go package path or directory path).
//
// It returns only directories that have Go files subject to
// the build constraints in env and logs a "Skipping package {}" statement
// about packages that are excluded due to build constraints.
//
// ResolveGlobs always returns either an absolute file system path and
// normalized Go package paths. The return list may be mixed.
//
// See NewPackages for allowed formats.
func ResolveGlobs(logger ulog.Logger, env golang.Environ, workingDirectory string, patterns []string) ([]string, error) {
	var dirIncludes []string
	var dirExcludes []string
	var gopathIncludes []string
	var gopathExcludes []string
	for _, pattern := range patterns {
		isExclude := strings.HasPrefix(pattern, "-")
		if isExclude {
			pattern = pattern[1:]
		}
		if matches, _ := filepath.Glob(pattern); len(matches) > 0 {
			if !isExclude {
				dirIncludes = append(dirIncludes, matches...)
			} else {
				dirExcludes = append(dirExcludes, matches...)
			}
		} else {
			if !isExclude {
				gopathIncludes = append(gopathIncludes, pattern)
			} else {
				gopathExcludes = append(gopathExcludes, pattern)
			}
		}
	}

	directories, err := filterDirectoryPaths(logger, env, dirIncludes, dirExcludes)
	if err != nil {
		return nil, err
	}

	gopaths, err := filterGoPaths(logger, env, workingDirectory, gopathIncludes, gopathExcludes)
	if err != nil {
		return nil, err
	}

	result := append(directories, gopaths...)
	if len(result) == 0 {
		return nil, errNoMatch
	}
	sort.Strings(result)
	return result, nil
}
