// Copyright 2018 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package main is the busybox main.go template.
package main

import (
	"errors"
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
	err := bbmain.Run(name)
	if errors.Is(err, bbmain.ErrNotRegistered) {
		if len(os.Args) > 1 {
			os.Args = os.Args[1:]
			err = bbmain.Run(filepath.Base(os.Args[0]))
		}
	}
	if errors.Is(err, bbmain.ErrNotRegistered) {
		log.SetFlags(0)
		log.Printf("Failed to run command: %v", err)

		log.Printf("Supported commands are:")
		for _, cmd := range bbmain.ListCmds() {
			log.Printf(" - %s", cmd)
		}
		os.Exit(1)
	} else if err != nil {
		log.SetFlags(0)
		log.Fatalf("Failed to run command: %v", err)
	}
}

func main() {
	os.Args[0] = ResolveUntilLastSymlink(os.Args[0])

	run()
}

// A gobusybox has 3 possible ways of invocation:
//
// ## Direct
//
//   ./bb ls -l
//
// For the gobusybox, argv in this case is ["./bb", "ls", "-l"] on all OS.
//
//
// ## Symlink
//
//   ln -s bb ls
//   ./ls
//
// For the gobusybox, argv in this case is ["./ls"] on all OS.
//
//
// ## Interpreted
//
// This way exists because Plan 9 does not have symlinks. Some Linux file
// system such as VFAT also do not support symlinks.
//
//   echo "#!/bin/bb #!gobb!#" >> /tmp/ls
//   /tmp/ls
//
// For the gobusybox, argv depends on the OS:
//
// Plan 9: ["ls", "#!gobb!#", "/tmp/ls"]
// Linux/Unix: ["/bin/bb", "#!gobb!#", "/tmp/ls"]
//
// Unix and Plan 9 evaluate arguments in a #! file differently, and, further,
// invoke the arguments in a different way.
//
// (1) The absolute path for /bin/bb is required, else Linux will throw an
//     error as bb is not in the list of allowed interpreters.
//
// (2) On Plan 9, the arguments following the interpreter are tokenized (split
//     on space) and on Linux, they are not. That means we should restrict
//     ourselves to only ever using one argument in the she-bang line (#!).
//
// (3) Which gobusybox tool to use is always in argv[2].
//
// (4) Because of the differences in how arguments are presented to #! on
//     different kernels, there should be a reasonably unique magic value so
//     that bb can have confidence that it is running as an interpreter, rather
//     than on the command-line in direct mode.
//
// The code needs to change the arguments to look like an exec: ["/tmp/ls", ...]
//
// In each case, the second arg must be "#!gobb!#", which is extremely
// unlikely to appear in any other context (save testing files, of course).
//
// The result is that the kernel, given a path to a #!gobb#! file, will
// read that file, then exec bin with the argument from argv[2] and any
// additional arguments from the exec.
func init() {
	// Interpreted mode: If this has been run from a #!gobb!# file, it
	// will have at least 3 args, and os.Args needs to be reconstructed.
	if len(os.Args) > 2 && os.Args[1] == "#!gobb!#" {
		os.Args = os.Args[2:]
	}

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
