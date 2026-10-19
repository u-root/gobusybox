// Package main checks that package-level vars survive the busybox rewrite.
//
// The rewrite moves every global initializer into a busybox init of its own,
// so that it runs only when this command is invoked. A var spec that binds
// several names needs care: the names can outnumber the values, they need not
// share a type, and the order the initializers run in is the init order
// go/types computed, not the order they appear here.
package main

import (
	"fmt"
	"os"
)

func twoStrings() (string, string) {
	return "foo", "bar"
}

func stringAndInt() (string, int) {
	return "qux", 7
}

func twoInts() (int, int) {
	return 10, 20
}

var m = map[string]int{"k": 42}

var (
	// Several names, one multi-valued call.
	foo, bar = twoStrings()

	// Several names, one value each.
	baz, fob = "baz", "fob"

	// Several names, one value each, but not a shared type.
	num, word = 1, "one"

	// Several names of differing type, from one call.
	qux, seven = stringAndInt()

	// The comma-ok form.
	val, ok = m["k"]

	// A blank identifier among the names.
	_, alsoSeven = stringAndInt()

	// An explicit type, shared by every name.
	lo, hi int = twoInts()
)

// sum reads x and y, which are declared after it. Go runs these in init
// order, so sum must still see 10 and 20 rather than two zeroes.
var sum = x + y

var x, y = twoInts()

// A multi-valued initializer that depends on an earlier one.
var dx, dy = x + 1, y + 1

func main() {
	fmt.Fprintln(os.Stdout,
		foo, bar, baz, fob, num, word, qux, seven, val, ok, alsoSeven, lo, hi,
		sum, x, y, dx, dy)
}
