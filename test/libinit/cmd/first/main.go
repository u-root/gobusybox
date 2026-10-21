// Command first reaches lonely, which nothing else in this fixture reaches,
// and shared, which the other command reaches too.
//
// It prints what their initialization left behind, because that is the only
// way to see whether it ran. Once a library's init becomes a function the
// dispatched command calls, it stops appearing in GODEBUG=inittrace=1
// whether it was called or silently skipped, so nothing but the output can
// tell those apart.
package main

import (
	"fmt"

	"github.com/u-root/gobusybox/test/libinit/pkg/lonely"
	"github.com/u-root/gobusybox/test/libinit/pkg/shared"
)

func main() {
	fmt.Println(lonely.FromInit, shared.FromInit)
}
