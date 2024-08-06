// Copyright 2015-2018 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// makebb compiles many Go commands into one bb-style binary.
package main

import (
	"flag"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"github.com/dustin/go-humanize"
	"github.com/u-root/gobusybox/src/pkg/bb"
	"github.com/u-root/gobusybox/src/pkg/golang"
)

var (
	outputPath = flag.String("o", "bb", "Path to compiled busybox binary")
	genDir     = flag.String("gen-dir", "", "Directory to generate source in")
	genOnly    = flag.Bool("g", false, "Generate but do not build binaries")
	keep       = flag.Bool("k", false, "Keep generated source temporary directory")
)

func main() {
	bopts := &golang.BuildOpts{}
	bopts.RegisterFlags(flag.CommandLine)
	env := golang.Default()
	env.RegisterFlags(flag.CommandLine)
	flag.Parse()

	// Why doesn't the log package export this as a default?
	l := log.New(os.Stdout, "", log.Ltime)

	o, err := filepath.Abs(*outputPath)
	if err != nil {
		l.Fatal(err)
	}

	if env.CgoEnabled {
		l.Printf("Disabling CGO for u-root...")
		env.CgoEnabled = false
	}

	err = env.InitCompiler()
	if err != nil {
		l.Fatal(err)
	}

	l.Printf("Build environment: %s", env)
	l.Printf("Compiler: %s", env.Compiler.VersionOutput)

	tmpDir := *genDir
	remove := false
	if tmpDir == "" {
		tdir, err := ioutil.TempDir("", "bb-")
		if err != nil {
			l.Fatalf("Could not create busybox source directory: %v", err)
		}
		tmpDir = tdir
		remove = true
	}

	opts := &bb.Opts{
		Env:          env,
		GenSrcDir:    tmpDir,
		CommandPaths: flag.Args(),
		BinaryPath:   o,
		GoBuildOpts:  bopts,
		GenerateOnly: *genOnly,
	}
	if err := bb.BuildBusybox(l, opts); err != nil {
		l.Fatalf("Preserving bb generated source directory at %s due to error: %v", tmpDir, err)
		// Only remove temp dir if there was no error.
		remove = false
	} else if opts.GenerateOnly {
		l.Printf("Generated source can be found in %s. `cd %s && go build` to build.", tmpDir, filepath.Join(tmpDir, "src/bb.u-root.com/bb"))
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
