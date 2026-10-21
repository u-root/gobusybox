// Package dependent imports shared, so Go initializes shared first, in full,
// before anything here runs. Deferring initialization has to preserve that
// order: the values below record what shared had already set, so running the
// two the wrong way round changes what the command prints rather than going
// unnoticed.
package dependent

import "github.com/u-root/gobusybox/test/libinit/pkg/shared"

// FromVar is set by a package-level variable initializer that reads what
// shared's init left behind, not merely shared's variable. That is the whole
// of the rule above: reading shared.Var would be satisfied by running
// shared's variables, then this one, then shared's init, which Go never
// does. Reading shared.FromInit instead means such an order leaves this
// "dependent.var(saw )".
var FromVar = "dependent.var(saw " + shared.FromInit + ")"

// FromInit reads what shared's own init produced, so it covers the order
// between two packages' init rather than between a variable and an init.
// It appends for the reason shared.FromInit does.
var FromInit string

func init() {
	FromInit += "dependent.init(saw " + shared.FromInit + ")"
}
