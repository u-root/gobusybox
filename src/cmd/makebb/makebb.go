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
)

func main() {
	bopts := &golang.BuildOpts{}
	bopts.RegisterFlags(flag.CommandLine)
	flag.Parse()

	// Why doesn't the log package export this as a default?
	l := log.New(os.Stdout, "", log.LstdFlags)

	o, err := filepath.Abs(*outputPath)
	if err != nil {
		l.Fatal(err)
	}

	env := golang.Default()
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
	}
	if err := bb.BuildBusybox(opts); err != nil {
		l.Print(err)
		if env.GO111MODULE == "off" {
			l.Fatalf("Preserving bb generated source directory at %s due to error.", tmpDir)
		} else {
			l.Fatalf("Preserving bb generated source directory at %s due to error. To debug build, you can `cd %s/src/bb.u-root.com/pkg` and use `go build` to build, or `go mod [why|tidy|graph]` to debug dependencies, or `go list -m all` to .", tmpDir)
		}
	}
	// Only remove temp dir if there was no error.
	if remove {
		os.RemoveAll(tmpDir)
	}
}
