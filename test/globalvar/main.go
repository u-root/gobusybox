package main

import (
	"fmt"
)

func Foobar() (string, string) {
	return "foo", "bar"
}

var (
	foo, bar = Foobar()
	baz, fob = "baz", "fob"
)

func main() {
	fmt.Println(foo, bar, baz, fob)
}
