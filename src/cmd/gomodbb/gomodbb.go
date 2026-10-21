// Copyright 2015-2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// gomodbb compiles many Go commands into one bb-style binary, fetching their
// source through the module proxy rather than requiring a checkout.
//
//	gomodbb github.com/u-root/u-root/cmds/core/...@main
//
// It does that by writing a module whose only purpose is to require what the
// patterns name, which is what turns a package pattern into the module that
// provides it. That module stands in for the checkout: it holds no source,
// only the go.mod and go.sum recording what the patterns resolved to, which
// is enough for the go command to list the packages and their versions.
// Several projects named at once resolve into one build list, which is the
// job a workspace would otherwise do. The source stays in the module cache,
// and the busybox is generated into a separate directory.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
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
		return p.fetchPath()
	}
	return p.fetchPath() + "@" + p.query
}

// couldBeGlob reports whether an element is a path.Match pattern rather than a
// literal. It is findpkg's test, repeated so that gomodbb and the build agree
// on which patterns are globs.
func couldBeGlob(s string) bool {
	return strings.ContainsAny(s, "*?[") || strings.Contains(s, `\\`)
}

// fetchPath returns the package pattern in a form the go command accepts.
//
// The go command knows "...", which descends, but not the "*" that matches a
// single element. Widening a glob to the "..." that contains it asks for a
// superset the go command can fetch and list, and matches narrow it back down.
// findpkg widens the same way for the same reason, so a globbed pattern
// selects the same packages here as it does during the build.
func (p pattern) fetchPath() string {
	elems := strings.Split(p.path, "/")
	for i, e := range elems {
		if couldBeGlob(e) {
			// elems[:i:i] so that appending copies rather than
			// overwriting the element the glob was found in.
			return strings.Join(append(elems[:i:i], "..."), "/")
		}
	}
	return p.path
}

// matches reports whether an import path is one this pattern selects.
//
// Only a widened pattern needs narrowing. Anything else was handed to the go
// command as written, so whatever came back already matched it.
func (p pattern) matches(pkgPath string) (bool, error) {
	if !couldBeGlob(p.path) {
		return true, nil
	}
	return path.Match(p.path, pkgPath)
}

// parsePattern splits a command-line argument into a package pattern and a
// version query.
//
// A Go package path cannot contain "@", so everything after the last one is
// the query. Splitting on the last rather than the first matters for a path
// that already carries a major version suffix, such as gopkg.in/yaml.v2.
func parsePattern(arg string) pattern {
	arg = strings.TrimSpace(arg)

	var p pattern
	if rest, ok := strings.CutPrefix(arg, "-"); ok {
		p.exclude = true
		arg = rest
	}

	p.path = arg
	if i := strings.LastIndex(arg, "@"); i >= 0 {
		p.path, p.query = arg[:i], arg[i+1:]
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

// goCmd returns a go command to run against the fetching module.
//
// Source comes from the module proxy, never from a version control tool.
// GOVCS is what establishes that, and it is the only setting that can: it
// refuses the fetch whatever the proxy is, and names the rule rather than a
// missing tool. That distinction matters because golang.Environ reduces PATH
// to $GOROOT/bin, so git is absent even when the caller has it, and the bare
// failure blames git instead of the policy:
//
//	go: github.com/u-root/uio@latest: unable to resolve git version:
//	failed to execute git version: exec: "git": executable file not found in $PATH
//
// GOPRIVATE and GONOPROXY are cleared because both name paths that bypass the
// proxy, and both are read from the caller's environment.
//
// GOPROXY is deliberately left alone. The go command's own default is
// https://proxy.golang.org,direct, so a caller who has set nothing already
// fetches through the public proxy: using gomodbb must never require setting
// GOPROXY, and does not. Pinning it would add nothing to the guarantee above,
// because GOVCS already refuses a VCS, while overriding a caller who has a
// proxy of their own -- a corporate mirror, or a cache standing in for the
// network so that a test can run offline.
//
// exec uses the last value for a repeated key, so these override the caller's.
func goCmd(env *golang.Environ, gocmd string, args ...string) *exec.Cmd {
	cmd := env.GoCmd(gocmd, args...)
	cmd.Env = append(cmd.Env,
		"GOPRIVATE=",
		"GONOPROXY=",
		"GOVCS=*:off",
	)
	return cmd
}

// checkCollisions reports command names claimed by more than one package.
//
// A busybox reaches each command by the base name of its directory, so a name
// claimed twice cannot be built whatever the import paths are. The build
// refuses these too; finding them here costs a listing rather than a fetch and
// a generated tree.
func checkCollisions(cmds []string) error {
	paths := map[string][]string{}
	for _, c := range cmds {
		name := path.Base(c)
		paths[name] = append(paths[name], c)
	}

	var names []string
	for name, p := range paths {
		if len(p) > 1 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)

	var b strings.Builder
	fmt.Fprintf(&b, "%d command name", len(names))
	if len(names) > 1 {
		b.WriteString("s")
	}
	b.WriteString(" claimed by more than one package, which will not build")
	for _, name := range names {
		fmt.Fprintf(&b, "\n\t%s: %s", name, strings.Join(paths[name], ", "))
	}
	b.WriteString("\n\nName the ones to leave out as exclusions, e.g. -")
	b.WriteString(paths[names[0]][0])
	return errors.New(b.String())
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

// writeGoMod gives modDir a go.mod, unless it already has one.
//
// Writing the file rather than running `go mod init` is what makes -mod-dir
// reusable: init refuses a directory that already has a go.mod, and that file
// is the record of which versions the last run resolved.
func writeGoMod(modDir string) error {
	gomod := filepath.Join(modDir, "go.mod")
	switch _, err := os.Stat(gomod); {
	case err == nil:
		return nil
	case !os.IsNotExist(err):
		return err
	}
	contents := fmt.Sprintf("module %s\n", synthModulePath)
	if err := os.WriteFile(gomod, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("writing go.mod: %w", err)
	}
	return nil
}

// proxyless reports whether a GOPROXY value names no module proxy at all.
//
// The value is a list separated by commas or pipes, whose entries are either
// a proxy URL or one of the keywords "direct" and "off". The go command's own
// default is "https://proxy.golang.org,direct", so a keyword in the list
// means nothing on its own -- only a list with no URL left in it leaves the
// go command nothing gomodbb is allowed to fetch from.
func proxyless(goproxy string) bool {
	if strings.TrimSpace(goproxy) == "" {
		// The go command falls back to its own default, which is a proxy.
		return false
	}
	for _, entry := range strings.FieldsFunc(goproxy, func(r rune) bool {
		return r == ',' || r == '|'
	}) {
		switch strings.TrimSpace(entry) {
		case "", "direct", "off":
			// A keyword, not somewhere to fetch from.
		default:
			return false
		}
	}
	return true
}

// proxylessWarning returns the warning to print for a GOPROXY value, or ""
// when the value names a proxy and there is nothing worth saying.
//
// It is separate from the lookup below so that the decision and the wording
// can be tested without running a go command.
func proxylessWarning(goproxy string) string {
	if !proxyless(goproxy) {
		return ""
	}
	return fmt.Sprintf("Warning: GOPROXY is %q, which names no module proxy.\n"+
		"  gomodbb fetches source through a proxy and refuses a version control\n"+
		"  tool, so only modules already in the module cache can be found.\n"+
		"  Unset GOPROXY to use the go command's default, or set it to a proxy.",
		goproxy)
}

// warnIfProxyless warns when GOPROXY leaves gomodbb nowhere to fetch from.
//
// gomodbb fetches through a module proxy, and GOVCS refuses a version control
// tool, so a GOPROXY of "direct" or "off" permits nothing. What the go
// command then reports is a VCS policy error naming neither gomodbb nor
// GOPROXY, which is a confusing place to start looking:
//
//	go: ...: GOVCS disallows using git for public github.com/u-root/u-root
//
// It warns rather than refusing, because the build can still succeed: a
// module already in the module cache is served from there without a proxy
// being consulted at all, which is how an offline build works.
//
// GOPROXY is read with `go env` rather than from the environment, so that a
// value set with `go env -w` is seen too, and so that an unset GOPROXY
// resolves to the default the fetch will really use.
func warnIfProxyless(l *log.Logger, env *golang.Environ) {
	out, err := goCmd(env, "env", "GOPROXY").Output()
	if err != nil {
		// The fetch reports anything that matters, so failing to look
		// this up is not worth stopping for.
		return
	}

	if w := proxylessWarning(strings.TrimSpace(string(out))); w != "" {
		l.Print(w)
	}
}

// fetch writes a module requiring everything the patterns name, leaving the
// source in the module cache and the chosen versions in its go.mod.
func fetch(l *log.Logger, env *golang.Environ, modDir string, patterns []pattern) error {
	if err := writeGoMod(modDir); err != nil {
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
	warnIfProxyless(l, env)
	l.Printf("Fetching %s", strings.Join(specs, " "))
	cmd := goCmd(env, "get", specs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("`go get %s`: %w: %s", strings.Join(specs, " "), err, out)
	}
	return nil
}

// commandLister reports the commands one pattern matches.
//
// listCommands takes it as a parameter so that which commands a pattern covers
// and which of them survive the exclusions stay separable: the first needs a
// module on disk to ask about, the second is arithmetic.
type commandLister func(p pattern) ([]string, error)

// listCommands returns the import paths the patterns match that are commands.
//
// This is the same question makebb answers while building, asked without
// building, so that a caller can see what a pattern covers before committing
// to it. Exclusions are applied here too, so the list matches what a build
// with these arguments would contain.
//
// Order follows the command line, because the caller wrote it in the order
// they did for a reason, and a command matched by two patterns keeps the
// position of the first.
func listCommands(patterns []pattern, list commandLister) ([]string, error) {
	excluded := map[string]bool{}
	var include []string
	for _, p := range patterns {
		paths, err := list(p)
		if err != nil {
			return nil, err
		}
		if p.exclude {
			for _, path := range paths {
				excluded[path] = true
			}
			continue
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
func mainPackages(env *golang.Environ, p pattern) ([]string, error) {
	cmd := goCmd(env, "list", "-e", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, p.fetchPath())
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("`go list %s`: %w", p.fetchPath(), err)
	}
	var paths []string
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch ok, err := p.matches(line); {
		case err != nil:
			return nil, fmt.Errorf("matching %q against %q: %w", p.path, line, err)
		case ok:
			paths = append(paths, line)
		}
	}
	return paths, nil
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
		cmds, err := listCommands(patterns, func(p pattern) ([]string, error) {
			return mainPackages(env, p)
		})
		if err != nil {
			l.Fatalf("Listing commands failed: %v", err)
		}
		for _, c := range cmds {
			fmt.Println(c)
		}
		// The list is what a build with these arguments would contain,
		// so a collision that would stop that build has to stop here
		// too. The list is printed first: it is the answer that was
		// asked for, and it is what the exclusions have to be chosen
		// from.
		if err := checkCollisions(cmds); err != nil {
			l.Fatal(err)
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
