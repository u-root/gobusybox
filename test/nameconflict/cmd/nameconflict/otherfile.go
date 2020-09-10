package main

import (
	l "log"

	// Purposely assign a different name in the same package, but in a different file.
	//
	// This pollutes the namespace of main.go's file scope, as well, but
	// `foolog` can only be _used_ in this file.
	foolog "github.com/u-root/gobusybox/test/nameconflict/pkg/defaultlog"

	// This should be possible -- while no variable can be named anotherlog
	// in this file, another import *can* be named that.
	anotherlog "math/rand"
)

var foologlog = foolog.Default()

// should conflict with init being rewritten.
func busyboxInit1() {
	l.Printf("busyboxInit1")
}

func init() {
	var foobar string
	foobar = "dog"
	l.Printf("Yes hello %d this is %s:", anotherlog.Int(), foobar)
}
