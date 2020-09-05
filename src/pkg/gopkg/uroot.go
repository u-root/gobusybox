// Copyright 2015-2017 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gopkg

/*import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/u-root/gobusybox/src/pkg/golang"
	"github.com/u-root/u-root/pkg/ulog"
)

func golistIfy(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	// "go list" sees a difference in "go list foobar/foo" and "go list
	// ./foobar/foo".
	return "./" + path
}

// resolvePackagePath finds import paths for a single import path/glob or directory string/glob.
func resolvePackagePath(logger ulog.Logger, env golang.Environ, pkg string) ([]string, error) {
	// Try the file system first.
	matches, _ := filepath.Glob(pkg)
	var importPaths []string
	for _, match := range matches {
		// Only match directories for building.
		// Skip anything that is not a directory
		fileInfo, _ := os.Stat(match)
		if !fileInfo.IsDir() {
			continue
		}

		p, err := env.FindOneCmd(golistIfy(match))
		if err != nil {
			logger.Printf("Skipping package %q: %v", match, err)
		} else if p.ImportPath == "." {
			// TODO: I do not completely understand why
			// this is triggered. This is only an issue
			// while this function is run inside the
			// process of a "go test".
			importPaths = append(importPaths, pkg)
		} else {
			importPaths = append(importPaths, p.ImportPath)
		}
	}

	var err error
	// Def not a filepath, so this must be a glob for Go package paths.
	if !filepath.IsAbs(pkg) && pkg[0:1] != "./" {
		var query string

		// Does this maybe contain a glob? See filepath.Match documentation.
		//
		// If so, search for "..." in the last component before the
		// glob shows up. E.g. if
		// github.com/u-root/u-root/cmds/*boot*, query Go for
		// github.com/u-root/u-root/cmds/..., and then use
		// filepath.Match to narrow it down.
		if i := strings.IndexAny(pkg, "?*["); i != -1 {
			// Cut off everything after the last / before the first *?[.
			//
			// Then append ... to get "go list -json" to tell you everything.
			s := strings.Split(pkg[:i], "/")
			prefix := strings.Join(s[:len(s)-1], "/")
			query = path.Join(prefix, "...")
		} else {
			query = pkg
		}

		var pkgs []*golang.Package
		pkgs, err = env.FindCmds(query)
		for _, p := range pkgs {
			var pkgPath string
			if p.ImportPath == "." {
				// TODO: I do not completely understand why
				// this is triggered. This is only an issue
				// while this function is run inside the
				// process of a "go test".
				pkgPath = pkg
			} else {
				pkgPath = p.ImportPath
			}

			if pkgPath[0] == '_' {
				// Package paths that being with _ are packages outside of the specified env.
				// Just ignore it.
			} else if strings.Contains(pkg, "...") {
				// ... is the Go package wildcard that filepath.Match doesn't support.
				importPaths = append(importPaths, pkgPath)
			} else if matched, err := filepath.Match(pkg, pkgPath); matched || err != nil {
				// If err != nil, then pkg is not a pattern. Just
				// accept the package in that case.
				importPaths = append(importPaths, pkgPath)
			}
		}
	}

	// No file import paths found. Check if pkg still resolves as a package name.
	if len(importPaths) == 0 {
		return nil, fmt.Errorf("%q is neither package or path/glob: %v", pkg, err)
	}
	return importPaths, nil
}

// ResolvePackagePaths takes a list of Go package import paths and directories
// and turns them into exclusively import paths.
//
// Currently allowed formats:
//
//   - package imports; e.g. github.com/u-root/u-root/cmds/ls
//   - globs of package imports, e.g. github.com/u-root/u-root/cmds/*
//   - paths to package directories; e.g. $GOPATH/src/github.com/u-root/u-root/cmds/ls
//   - globs of paths to package directories; e.g. ./cmds/*
//   - if an entry starts with "-" it excludes the matching package(s)
//
// Directories may be relative or absolute, with or without globs.
// Globs are resolved using filepath.Glob.
func ResolvePackagePaths(logger ulog.Logger, env golang.Environ, pkgs []string) ([]string, error) {
	var includes []string
	excludes := map[string]bool{}
	for _, pkg := range pkgs {
		isExclude := false
		if strings.HasPrefix(pkg, "-") {
			pkg = pkg[1:]
			isExclude = true
		}
		paths, err := resolvePackagePath(logger, env, pkg)
		if err != nil {
			return nil, err
		}
		if !isExclude {
			includes = append(includes, paths...)
		} else {
			for _, p := range paths {
				excludes[p] = true
			}
		}
	}
	var result []string
	for _, p := range includes {
		if !excludes[p] {
			result = append(result, p)
		}
	}
	return result, nil
}*/
