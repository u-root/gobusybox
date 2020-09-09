package main

import (
	crand "crypto/rand"
	mrand "math/rand"

	hellolog "github.com/u-root/gobusybox/src/bazeltest/pkg/log"
	byelog "github.com/u-root/gobusybox/src/bazeltest/pkg/log/log"
)

// Global variables' type declarations get rewritten by busybox, which is why
// these are globals.
var (
	Rand   = mrand.New(mrand.NewSource(99))
	Crypto = crand.Reader
	Hello  = hellolog.Hello()
	Bye    = byelog.Bye()
)

func main() {}
