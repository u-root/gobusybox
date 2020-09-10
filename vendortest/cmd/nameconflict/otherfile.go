package main

import (
	l "log"

	// Purposely assign a different name in the same package, but in a different file.
	//
	// This pollutes the namespace of main.go's file scope, as well, but
	// `foolog` can only be _used_ in this file.
	foolog "github.com/u-root/gobusybox/vendortest/pkg/nameconflict"

	// This should be possible -- while no variable can be named anotherlog
	// in this file, another import *can* be named that.
	anotherlog "github.com/u-root/gobusybox/vendortest/pkg/defaultlog"
)

// Dirent is an implicit import of golang.org/x/sys/unix. Do this to make sure
// vendored package names are handled correctly.
var Dirent = anotherlog.SomeDirent

var foologlog = foolog.Default()

// should conflict with init being rewritten.
func busyboxInit1() {
	l.Printf("busyboxInit1")
}

func init() {
	var foobar string
	foobar = "dog"
	l.Printf("Yes hello this is %s:", foobar)
}

func registeredMain() {
	l.Printf("registered main!")
}

func registeredInit() {
	l.Printf("registered init!")
}
