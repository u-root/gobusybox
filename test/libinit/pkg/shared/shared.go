// Package shared is reached by both commands in this fixture, so whichever
// one is dispatched, its initialization still has to have run.
package shared

// Var is set by a package-level variable initializer. Go runs every one of
// those in a package before any of that package's init, so FromInit below
// can see this.
var Var = "shared.var"

// FromInit records what init could see when it ran. Were the variable
// initializer not to run first, this would read "shared.init(saw )".
//
// init appends rather than assigns, because second reaches this package
// twice over: directly, and again through dependent. Assigning would leave
// a second run of init indistinguishable from one, so a rewrite that moved
// initialization into a hook and lost its once-only guard would still print
// the expected output. Appending makes the second run show up twice.
var FromInit string

func init() {
	FromInit += "shared.init(saw " + Var + ")"
}
