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

	"github.com/u-root/gobusybox/src/pkg/bb"
	"github.com/u-root/gobusybox/src/pkg/golang"
)

var (
	outputPath = flag.String("o", "bb", "Path to compiled busybox binary")
	genDir     = flag.String("gen-dir", "", "Directory to generate source in")
	genOnly    = flag.Bool("g", false, "Generate but do not build binaries")
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
	l.Printf("Build environment: %s", env)

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
	} else if opts.GenerateOnly {
		l.Printf("Generated source can be found in %s. `cd %s && go build` to build.", tmpDir, filepath.Join(tmpDir, "src/bb.u-root.com/bb"))
	}
	// Only remove temp dir if there was no error.
	if remove && !opts.GenerateOnly {
		os.RemoveAll(tmpDir)
	}
}
