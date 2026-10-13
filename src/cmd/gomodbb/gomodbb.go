// Copyright 2015-2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// gomodbb compiles many Go commands into one bb-style binary, fetching their
// source through the module proxy rather than requiring a checkout.
//
// makebb builds commands that are already on disk, which means every input has
// to be cloned first, and commands from several projects have to be arranged
// into a Go workspace before any of them can be named. Neither step says
// anything about the busybox being built. The go command already knows how to
// fetch a module and hand back its source, so gomodbb asks it to:
//
//	gomodbb github.com/u-root/u-root/cmds/core/...@main
//
// It does that by writing a module whose only purpose is to require what the
// patterns name, which is what turns a package pattern into the module that
// provides it. That module is the go command's job, not this one's: there is
// no table of known projects here, and adding one would be a bug.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dustin/go-humanize"
	"github.com/u-root/gobusybox/src/pkg/bb"
	"github.com/u-root/gobusybox/src/pkg/golang"
)

var (
	outputPath = flag.String("o", "bb", "Path to compiled busybox binary")
	genDir     = flag.String("gen-dir", "", "Directory to generate source in")
	genOnly    = flag.Bool("g", false, "Generate but do not build binaries")
	keep       = flag.Bool("k", false, "Keep generated source temporary directory")
	list       = flag.Bool("l", false, "List the commands the patterns match, and build nothing")
	keepModule = flag.String("mod-dir", "", "Write the module that requires the patterns here, and keep it")
)

// synthModulePath is the module written to require the requested patterns.
//
// Nothing imports it and it is never published, so the path only has to be
// one no input could also declare.
const synthModulePath = "gomodbb.u-root.com/fetch"

// pattern is one command-line argument, split into the part naming packages
// and the part naming a version.
type pattern struct {
	// path is the package pattern, with any version query removed. This is
	// what makebb is given, because by then the module requirement decides
	// the version and repeating it would be a second, disagreeing source of
	// truth.
	path string

	// query is the version query, without the "@". Empty means the caller
	// gave none, which leaves the choice to `go get`, namely the highest
	// release tag.
	query string

	// exclude reports whether the pattern removes commands rather than
	// adding them. Exclusions name packages the other patterns already
	// fetched, so fetching them again would be wrong as well as useless.
	exclude bool
}

// fetchSpec is the argument `go get` is given for this pattern.
func (p pattern) fetchSpec() string {
	if p.query == "" {
		return p.path
	}
	return p.path + "@" + p.query
}

// parsePattern splits a command-line argument into a package pattern and a
// version query.
//
// A Go package path cannot contain "@", so everything after the last one is
// the query. Splitting on the last rather than the first matters for a path
// that already carries a major version suffix, such as gopkg.in/yaml.v2.
func parsePattern(arg string) pattern {
	var p pattern
	if rest, ok := strings.CutPrefix(arg, "-"); ok {
		p.exclude = true
		arg = rest
	}
	if i := strings.LastIndex(arg, "@"); i >= 0 {
		p.path, p.query = arg[:i], arg[i+1:]
	} else {
		p.path = arg
	}
	return p
}

// commandPath returns the pattern as makebb takes it, exclusion marker and all.
func (p pattern) commandPath() string {
	if p.exclude {
		return "-" + p.path
	}
	return p.path
}

func main() {
	bopts := &golang.BuildOpts{}
	bopts.RegisterFlags(flag.CommandLine)
	env := golang.Default()
	env.RegisterFlags(flag.CommandLine)
	flag.Parse()

	// Diagnostics go to stderr, unlike makebb, which writes them to stdout.
	// -l exists to be piped into something, and a progress line landing in
	// the middle of the list it printed would defeat that.
	l := log.New(os.Stderr, "", log.Ltime)

	if flag.NArg() == 0 {
		l.Fatalf("Usage: %s [flags] <pattern>[@version]...\n\ne.g. %s github.com/u-root/u-root/cmds/core/...@main",
			filepath.Base(os.Args[0]), filepath.Base(os.Args[0]))
	}

	var patterns []pattern
	for _, arg := range flag.Args() {
		patterns = append(patterns, parsePattern(arg))
	}

	o, err := filepath.Abs(*outputPath)
	if err != nil {
		l.Fatal(err)
	}

	if env.CgoEnabled {
		l.Printf("Disabling CGO for u-root...")
		env.CgoEnabled = false
	}
	if err := env.CompilerInit(); err != nil {
		l.Fatal(err)
	}

	l.Printf("Build environment: %s\n", env)
	l.Printf("Compiler: %s\n", env.Compiler.VersionOutput)

	modDir, removeModDir, err := moduleDir(*keepModule)
	if err != nil {
		l.Fatal(err)
	}
	if removeModDir {
		defer os.RemoveAll(modDir)
	}

	// Every go invocation from here on runs in the module just created,
	// and must ignore any workspace that happens to enclose it: a go.work
	// above the temporary directory would otherwise decide the build list
	// for a module that is not one of its members.
	env.Apply(golang.WithWorkingDir(modDir), golang.WithGOWORK("off"))

	if err := fetch(l, env, modDir, patterns); err != nil {
		l.Fatalf("Fetching source failed: %v", err)
	}

	var commandPaths []string
	for _, p := range patterns {
		commandPaths = append(commandPaths, p.commandPath())
	}

	if *list {
		cmds, err := listCommands(env, patterns)
		if err != nil {
			l.Fatalf("Listing commands failed: %v", err)
		}
		for _, c := range cmds {
			fmt.Println(c)
		}
		return
	}

	tmpDir := *genDir
	remove := false
	if tmpDir == "" {
		tdir, err := os.MkdirTemp("", "bb-")
		if err != nil {
			l.Fatalf("Could not create busybox source directory: %v", err)
		}
		tmpDir = tdir
		remove = true
	}

	opts := &bb.Opts{
		Env:          env,
		GenSrcDir:    tmpDir,
		CommandPaths: commandPaths,
		BinaryPath:   o,
		GoBuildOpts:  bopts,
		GenerateOnly: *genOnly,
	}
	if err := bb.BuildBusybox(l, opts); err != nil {
		remove = false
		l.Fatalf("Preserving bb generated source directory at %s due to error: %v", tmpDir, err)
	} else if opts.GenerateOnly {
		l.Printf("Generated source can be found in %s. `cd %s && GOWORK=off go build -mod=vendor` to build.", tmpDir, tmpDir)
	}
	if remove && !opts.GenerateOnly && !*keep {
		os.RemoveAll(tmpDir)
	} else {
		l.Printf("Keeping temp dir %v", tmpDir)
	}

	path := *outputPath
	if stat, err := os.Stat(path); err == nil {
		if stat.IsDir() {
			path = filepath.Join(path, "bb")
			stat, err = os.Stat(path)
			if err != nil {
				return
			}
		}
		l.Printf("Successfully built %q (size %d bytes -- %s).", path, stat.Size(), humanize.IBytes(uint64(stat.Size())))
	}
}

// goCmd returns a go command to run against the fetching module.
//
// golang.Environ sets PATH to $GOROOT/bin alone, so that the chosen toolchain
// is the one x/tools finds. Nothing makebb runs looks further than that,
// because compiling shells out to nothing. Fetching does: the go command runs
// version control tools for any module the proxy cannot serve, which covers
// private modules and everything at all under GOPROXY=direct, and those live
// on the caller's PATH:
//
//	go: github.com/u-root/uio@latest: unable to resolve git version:
//	failed to execute git version: exec: "git": executable file not found in $PATH
//
// Keep $GOROOT/bin first so the toolchain still wins, and restore the rest
// behind it. exec uses the last value for a repeated key, so this overrides
// what Environ set without disturbing the other variables.
func goCmd(env *golang.Environ, gocmd string, args ...string) *exec.Cmd {
	cmd := env.GoCmd(gocmd, args...)
	path := os.Getenv("PATH")
	if env.GOROOT != "" {
		gorootBin := filepath.Join(env.GOROOT, "bin")
		if path == "" {
			path = gorootBin
		} else {
			path = gorootBin + string(os.PathListSeparator) + path
		}
	}
	if path != "" {
		cmd.Env = append(cmd.Env, "PATH="+path)
	}
	return cmd
}

// moduleDir returns the directory to write the fetching module into, and
// whether the caller should delete it afterwards.
func moduleDir(keep string) (string, bool, error) {
	if keep != "" {
		if err := os.MkdirAll(keep, 0o755); err != nil {
			return "", false, fmt.Errorf("creating module directory: %w", err)
		}
		abs, err := filepath.Abs(keep)
		return abs, false, err
	}
	dir, err := os.MkdirTemp("", "gomodbb-")
	if err != nil {
		return "", false, fmt.Errorf("creating module directory: %w", err)
	}
	return dir, true, nil
}

// fetch writes a module requiring everything the patterns name, leaving the
// source in the module cache and the chosen versions in its go.mod.
//
// Writing go.mod directly rather than running `go mod init` keeps this correct
// when -mod-dir is reused: init fails on a directory that already has one.
func fetch(l *log.Logger, env *golang.Environ, modDir string, patterns []pattern) error {
	gomod := filepath.Join(modDir, "go.mod")
	if _, err := os.Stat(gomod); os.IsNotExist(err) {
		contents := fmt.Sprintf("module %s\n", synthModulePath)
		if err := os.WriteFile(gomod, []byte(contents), 0o644); err != nil {
			return fmt.Errorf("writing go.mod: %w", err)
		}
	} else if err != nil {
		return err
	}

	var specs []string
	for _, p := range patterns {
		if p.exclude {
			continue
		}
		specs = append(specs, p.fetchSpec())
	}
	if len(specs) == 0 {
		return fmt.Errorf("every pattern is an exclusion, so there is nothing to fetch")
	}

	// One invocation rather than one per pattern, so that the go command
	// resolves all of them against a single build list. Fetching them
	// separately lets an earlier pattern pick a dependency version that a
	// later one then has to upgrade, doing the work twice.
	l.Printf("Fetching %s", strings.Join(specs, " "))
	cmd := goCmd(env, "get", specs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("`go get %s`: %w: %s", strings.Join(specs, " "), err, out)
	}
	return nil
}

// listCommands returns the import paths the patterns match that are commands.
//
// This is the same question makebb answers while building, asked without
// building, so that a caller can see what a pattern covers before committing
// to it. Exclusions are applied here too, so the list matches what a build
// with these arguments would contain.
func listCommands(env *golang.Environ, patterns []pattern) ([]string, error) {
	excluded := map[string]bool{}
	var include []string
	for _, p := range patterns {
		if p.exclude {
			paths, err := mainPackages(env, p.path)
			if err != nil {
				return nil, err
			}
			for _, path := range paths {
				excluded[path] = true
			}
			continue
		}
		paths, err := mainPackages(env, p.path)
		if err != nil {
			return nil, err
		}
		include = append(include, paths...)
	}

	seen := map[string]bool{}
	var out []string
	for _, path := range include {
		if excluded[path] || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out, nil
}

// mainPackages lists the import paths of the commands a single pattern matches.
//
// -e keeps a pattern that matches some unbuildable package from failing the
// whole listing, matching makebb, which skips what it cannot build rather than
// refusing to build anything.
func mainPackages(env *golang.Environ, path string) ([]string, error) {
	cmd := goCmd(env, "list", "-e", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, path)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("`go list %s`: %w", path, err)
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}
