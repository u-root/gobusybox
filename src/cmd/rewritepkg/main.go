// Copyright 2015-2018 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// rewritepkg takes a Go command's source and rewrites it to be a u-root
// busybox compatible library package.
package main

import (
	"flag"
	"fmt"
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

	sourceFiles      uflag.Strings
	stdlibZip        uflag.Strings
	unmappedArchives uflag.Strings
	stdlibArchives   uflag.Strings
	mappedArchives   uflag.Strings
)

func init() {
	flag.Var(&stdlibZip, "stdlib_zip", "(blaze) Go standard library zip archives containing stdlib object files")
	flag.Var(&unmappedArchives, "unmapped_archive", "(blaze) Go .a archives file paths for every dependency, where file path == import path")
	flag.Var(&stdlibArchives, "stdlib_archive", "(bazel) Go standard library directory or paths for .a files")
	flag.Var(&mappedArchives, "mapped_archive", "(bazel) list of goImportPath:goArchiveFilePath for every dependency")
	flag.Var(&sourceFiles, "source", "Source files")
}

func main() {
	flag.Parse()

	if len(*name) == 0 {
		log.Fatal("rewritepkg: no command name given")
	} else if len(*destDir) == 0 {
		log.Fatal("rewritepkg: no directory given")
	} else if len(*bbImportPath) == 0 {
		log.Fatal("rewritepkg: no bb import path given")
	}

	// bazel must pass stdlibArchives + mappedArchives.
	//
	// blaze must pass stdlibZip + unmappedArchives.

	if (len(stdlibZip) == 0 && len(stdlibArchives) == 0) || (len(stdlibZip) > 0 && len(stdlibArchives) > 0) {
		log.Fatal("Must pass exactly one kind of stdlib option -- either --stdlib_zip or --stdlib_archive, " +
			"but not neither nor both. More than one occurence of the chosen option is valid.")
	}
	if len(unmappedArchives) > 0 && len(mappedArchives) > 0 {
		log.Fatal("Cannot pass both --mapped_archive and --unmapped_archive.")
	}
	// This is not a technical limitation -- this is just to make sure the
	// Starlark rules pass the right stuff.
	if len(unmappedArchives) > 0 && len(stdlibArchives) > 0 {
		log.Fatal("Cannot combine --unmapped_archive with --stdlib_archive.")
	}
	if len(mappedArchives) > 0 && len(stdlibZip) > 0 {
		log.Fatal("Cannot combin --mappedA_rchive with --stdlib_zip.")
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
	var unmatchedGofiles []string
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
			unmatchedGofiles = append(unmatchedGofiles, path)
		}
	}

	imp, err := monoimporter.NewFromZips(c,
		[]string(unmappedArchives),
		[]string(mappedArchives),
		[]string(stdlibArchives),
		[]string(stdlibZip))
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

	for _, path := range unmatchedGofiles {
		_, basename := filepath.Split(path)
		content := fmt.Sprintf("package %s\n", bbPkg.PackageName())

		// N.B. Hack: Blaze expects an output file for every
		// input file, even if we decide to do nothing with the
		// input file. Just write a damn (near) empty file.
		if err := ioutil.WriteFile(filepath.Join(*destDir, basename), []byte(content), 0644); err != nil {
			log.Fatalf("Write empty file: %v", err)
		}
	}
}
