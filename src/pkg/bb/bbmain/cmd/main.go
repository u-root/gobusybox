// Copyright 2018 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package main is the busybox main.go template.
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/u-root/gobusybox/src/pkg/bb/bbmain"
	// There MUST NOT be any other dependencies here.
	//
	// It is preferred to copy minimal code necessary into this file, as
	// dependency management for this main file is... hard.
)

// AbsSymlink returns an absolute path for the link from a file to a target.
func AbsSymlink(originalFile, target string) string {
	if !filepath.IsAbs(originalFile) {
		var err error
		originalFile, err = filepath.Abs(originalFile)
		if err != nil {
			// This should not happen on Unix systems, or you're
			// already royally screwed.
			log.Fatalf("could not determine absolute path for %v: %v", originalFile, err)
		}
	}
	// Relative symlinks are resolved relative to the original file's
	// parent directory.
	//
	// E.g. /bin/defaultsh -> ../bbin/elvish
	if !filepath.IsAbs(target) {
		return filepath.Join(filepath.Dir(originalFile), target)
	}
	return target
}

// IsTargetSymlink returns true if a target of a symlink is also a symlink.
func IsTargetSymlink(originalFile, target string) bool {
	s, err := os.Lstat(AbsSymlink(originalFile, target))
	if err != nil {
		return false
	}
	return (s.Mode() & os.ModeSymlink) == os.ModeSymlink
}

// ResolveUntilLastSymlink resolves until the last symlink.
//
// This is needed when we have a chain of symlinks and want the last
// symlink, not the file pointed to (which is why we don't use
// filepath.EvalSymlinks)
//
// I.e.
//
// /foo/bar -> ../baz/foo
// /baz/foo -> bla
//
// ResolveUntilLastSymlink(/foo/bar) returns /baz/foo.
func ResolveUntilLastSymlink(p string) string {
	for target, err := os.Readlink(p); err == nil && IsTargetSymlink(p, target); target, err = os.Readlink(p) {
		p = AbsSymlink(p, target)
	}
	return p
}

func run() {
	name := filepath.Base(os.Args[0])
	if err := bbmain.Run(name); err != nil {
		log.Fatalf("%s: %v", name, err)
	}
}

func main() {
	os.Args[0] = ResolveUntilLastSymlink(os.Args[0])

	run()
}

func init() {
	m := func() {
		if len(os.Args) == 1 {
			log.Fatalf("Invalid busybox command: %q", os.Args)
		}
		// Use argv[1] as the name.
		os.Args = os.Args[1:]
		run()
	}
	bbmain.Register("bbdiagnose", bbmain.Noop, bbmain.ListCmds)
	bbmain.RegisterDefault(bbmain.Noop, m)
}
