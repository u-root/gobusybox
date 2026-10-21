// Command second reaches dependent, and through it shared, which it also
// imports directly. It never reaches lonely, so lonely's initialization is
// work this command pays for today and should not have to.
package main

import (
	"fmt"

	"github.com/u-root/gobusybox/test/libinit/pkg/dependent"
	"github.com/u-root/gobusybox/test/libinit/pkg/shared"
)

func main() {
	fmt.Println(dependent.FromVar, dependent.FromInit, shared.FromInit)
}
