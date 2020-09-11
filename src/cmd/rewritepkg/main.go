// Copyright 2015-2018 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// rewritepkg takes a Go command's source and rewrites it to be a u-root
// busybox compatible library package.
package main

import (
	"flag"
	"go/build"
	"io/ioutil"
	"log"
	"path/filepath"

	"github.com/u-root/gobusybox/src/pkg/bb/bbinternal"
	"github.com/u-root/gobusybox/src/pkg/monoimporter"
	"github.com/u-root/gobusybox/src/pkg/uflag"
)

var (
	name          = flag.String("name", "", "Name of the command")
	pkg           = flag.String("package", "", "Go import package path")
	destDir       = flag.String("dest_dir", "", "Destination directory")
	goarch        = flag.String("goarch", "", "override GOARCH of the resulting busybox")
	goos          = flag.String("goos", "", "override GOOS of the resulting busybox")
	installSuffix = flag.String("install_suffix", "", "override installsuffix of the resulting busybox")
	bbImportPath  = flag.String("bb_import_path", "", "BB import path")
	gorootDir     uflag.Strings
	archives      uflag.Strings
	sourceFiles   uflag.Strings
)

func init() {
	flag.Var(&gorootDir, "go_root_zip", "Go standard library zip archives containing stdlib object files")
	flag.Var(&archives, "archive", "Archives")
	flag.Var(&sourceFiles, "source", "Source files")
}

func main() {
	flag.Parse()

	if len(*name) == 0 {
		log.Fatal("rewritepkg: no command name given")
	} else if len(*destDir) == 0 {
		log.Fatal("rewritepkg: no directory given")
	}

	c := build.Default
	if *goarch != "" {
		c.GOARCH = *goarch
	}
	if *goos != "" {
		c.GOOS = *goos
	}
	if *installSuffix != "" {
		c.InstallSuffix = *installSuffix
	}

	var gofiles []string
	for _, path := range sourceFiles {
		dir, basename := filepath.Split(path)
		// Check the file against build tags.
		//
		// TODO: build.Default may not be the actual build environment.
		// Fix it via flags from Skylark?
		ok, err := c.MatchFile(dir, basename)
		if ok {
			gofiles = append(gofiles, path)
		} else if err != nil {
			log.Fatalf("MatchFile failed: %v", err)
		} else {
			b, err := ioutil.ReadFile(path)
			if err != nil {
				log.Fatal(err)
			}
			// N.B. Hack: Blaze expects an output file for every
			// input file, even if we decide to do nothing with the
			// input file.  Just write a damn empty file. The
			// compiler will automagically exclude it based on the
			// same build tags.
			if err := ioutil.WriteFile(filepath.Join(*destDir, basename), b, 0644); err != nil {
				log.Fatalf("Write empty file: %v", err)
			}
		}
	}

	imp, err := monoimporter.NewFromZips(c, []string(archives), []string(gorootDir))
	if err != nil {
		log.Fatal(err)
	}

	p, err := monoimporter.Load(*pkg, gofiles, imp)
	if err != nil {
		log.Fatal(err)
	}

	bbPkg := bbinternal.NewPackage(*name, p)
	if err := bbPkg.Rewrite(*destDir, *bbImportPath); err != nil {
		log.Fatalf("Rewriting failed: %v", err)
	}
}
