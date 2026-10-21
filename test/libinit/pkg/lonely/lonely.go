// Package lonely is reached by one command only, which is the ordinary case
// rather than an odd one: of the 228 library packages in the 120-command
// u-root busybox, 125 are reached by exactly one command, and today every
// command pays for all of them.
package lonely

// Var is set by a package-level variable initializer, before any init here.
var Var = "lonely.var"

// FromInit is set by init. A command that does not import this package must
// not run either of them; a command that does import it must run both, and
// must run them once. It appends for the reason shared.FromInit does.
var FromInit string

func init() {
	FromInit += "lonely.init(saw " + Var + ")"
}
