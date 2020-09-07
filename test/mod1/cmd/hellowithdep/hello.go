// hellowithdep has an internal and external dependency, as well as an external dependency that depends on internal code.
package main

import (
	"fmt"

	"github.com/u-root/gobusybox/test/mod1/pkg/hello"
	"github.com/u-root/gobusybox/test/mod2/pkg/exthello"
	hello2 "github.com/u-root/gobusybox/test/mod2/pkg/hello"

	// A package whose import path does not match its $GOPATH.
	hello4 "github.com/u-root/gobusybox/test/mod4/v2/pkg/hello"
)

func main() {
	fmt.Printf("test/mod1/hello: %s\n", hello.Hello())
	fmt.Printf("test/mod2/hello: %s\n", hello2.Hello())
	fmt.Printf("test/mod2/exthello: %s\n", exthello.Hello())
	fmt.Printf("test/mod4/hello: %s\n", hello4.Hello())
}
