package main

import (
	"github.com/u-root/gobusybox/src/bazeltest/pkg/defaultlog"
)

// Default returns a *log.Logger, but "log" is not imported in this package.
//
// The busybox build must add "log" to the import statements.
var l = defaultlog.Default()

// Call it twice to make sure we do not add the new import twice.
var l2 = defaultlog.Default()

// Dirent is an implicit dependency on golang.org/x/sys/unix. (We do this
// because non-stdlib dependencies are different from stdlib dependencies.)
var Dirent = defaultlog.SomeDirent

func main() {
	l.Printf("Log Hello")
}
