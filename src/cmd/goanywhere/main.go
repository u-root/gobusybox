// Copyright 2024 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// goanywhere creates a Go workspace from the packages' modules in the given
// local file paths.
//
// goanywhere then executes the command given after "--" in the workspace
// directory and amends the packages as args.
//
// E.g. goanywhere ./u-root/cmds/core/{init,gosh} -- go build -o $(pwd)
// E.g. goanywhere ./u-root/cmds/core/{init,gosh} -- makebb -o $(pwd)
// E.g. goanywhere ./u-root/cmds/core/{init,gosh} -- mkuimage [other args]
// E.g. goanywhere ./u-root/cmds/core/{init,gosh} -- u-root [other args]
//
// Like makebb, goanywhere supports GBB_PATH, globs and shell expansions.
//
// Usage: goanywhere [$cmd_path]... -- builder
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/u-root/gobusybox/src/pkg/bb/findpkg"
	"github.com/u-root/gobusybox/src/pkg/golang"
	"github.com/u-root/uio/ulog"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
	"golang.org/x/mod/modfile"
)

var (
	debug       = flag.Bool("d", false, "Show debug string")
	tmp         = flag.Bool("tmp", true, "Create workspace in temp dir")
	versionFlag = flag.String("v", "", "Go version for workspace file")
)

func run(dir string, args []string, paths []string) {
	var relPaths []string
	absDir, err := filepath.Abs(dir)
	if err != nil {
		relPaths = paths
	} else {
		for _, p := range paths {
			rp, err := filepath.Rel(absDir, p)
			if err != nil || len(rp) >= len(p) {
				relPaths = append(relPaths, p)
			} else {
				relPaths = append(relPaths, "./"+rp)
			}
		}
	}
	if *debug {
		if dir != "" {
			fmt.Printf("+ cd %s\n", dir)
		}
		fmt.Printf("+ %s %s\n", strings.Join(args, " "), strings.Join(relPaths, " "))
	}
	c := exec.Command(args[0], append(args[1:], relPaths...)...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	c.Dir = dir
	if err := c.Run(); err != nil {
		log.Fatalf("Exited with %v", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// moduleGoVersion returns the go directive of the go.mod in dir, or "" if it
// declares none.
func moduleGoVersion(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", err
	}
	f, err := modfile.ParseLax(filepath.Join(dir, "go.mod"), b, nil)
	if err != nil {
		return "", err
	}
	if f.Go == nil {
		return "", nil
	}
	return f.Go.Version, nil
}

func main() {
	flag.Parse()
	log.SetPrefix("[goanywhere] ")

	args := flag.Args()
	if len(args) == 0 {
		log.Fatalf("Usage: goanywhere $pkg... -- <command> ...")
	}
	dashIndex := slices.Index(args, "--")
	if dashIndex == -1 {
		log.Fatalf("Must use -- to distinguish Go packages from command. Usage: goanywhere $pkg... -- <command> ...")
	}
	if len(args) == dashIndex+1 {
		log.Fatalf("Must provide command to run. Usage: goanywhere $pkg... -- <command> ...")
	}
	pkgs := args[:dashIndex]
	args = args[dashIndex+1:]

	env := golang.Default()
	paths := findpkg.GlobPaths(ulog.Log, findpkg.DefaultEnv(), pkgs...)
	mods, noModulePaths := findpkg.Modules(paths)

	if env.GO111MODULE == "off" {
		log.Fatalf("GO111MODULE=off is not supported: goanywhere exists to generate a go.work, which GOPATH mode cannot use. Unset GO111MODULE, or set it to on.")
	}

	if len(noModulePaths) > 0 {
		log.Fatalf("No modules found for %v", noModulePaths)
	}

	var dir string
	if *tmp {
		var err error
		dir, err = os.MkdirTemp("", "goanywhere-")
		if err != nil {
			log.Fatalf("Could not create temp dir: %v", err)
		}
		defer os.RemoveAll(dir)
	}

	if *versionFlag == "" {
		v, err := env.Version()
		if err != nil {
			v = runtime.Version()
		}
		*versionFlag = golang.MajorMinorGoVersion(v, "")
		if *versionFlag == "" {
			log.Fatalf("Could not determine version from %v (set a version with -v flag)", v)
		}
	}

	// A go.work must declare a go version at least as high as every module
	// it uses, or the go tool refuses to load the workspace with
	// "module X requires go >= Y, but go.work lists go Z".
	//
	// This runs even when -v was given. An explicit version is a baseline,
	// not a ceiling: honouring "-v 1.25" against a member declaring
	// "go 1.26.6" would emit a workspace the go tool rejects outright.
	for dir := range mods {
		mv, err := moduleGoVersion(dir)
		if err != nil {
			log.Fatalf("Could not read go.mod in %s: %v", dir, err)
		}
		if golang.CompareGoVersions(mv, *versionFlag) > 0 {
			*versionFlag = mv
		}
	}

	tpl := `go {{.Version}}

use ({{range .Modules}}
	{{.}}{{end}}
)
`

	vars := struct {
		Version string
		Modules []string
	}{
		Version: *versionFlag,
		Modules: maps.Keys(mods),
	}
	t := template.Must(template.New("tpl").Parse(tpl))
	var b bytes.Buffer
	if err := t.Execute(&b, vars); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.work"), b.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	defer os.Remove(filepath.Join(dir, "go.work"))

	run(dir, args, paths)
}
