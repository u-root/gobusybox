# Exactly one command runs per busybox process

A busybox dispatches to one command and never comes back. `bbmain.Run` calls
the command's registered init, then its registered main, then `os.Exit(0)`.
Nothing in the generated main program and nothing in any command re-enters the
registry. This has been true since the registry was written, but nothing
depended on it and nothing said so.

Go Busybox now depends on it, so it is written down here and enforced rather
than left to be rediscovered.
[0004](0004-library-init-runs-for-the-command-that-needs-it.md) defers a
library's `init` until the command that needs it is dispatched, and that is
only equivalent to what Go does if no second command follows in the same
process. Go runs a package's `init` once per program; deferring it to dispatch
runs it once per dispatched command. Those two are the same count only while a
process dispatches once.

The guard belongs on the path that runs a command, not at the entry to
`bbmain.Run`. The generated main program calls `Run` twice on purpose: on
anything but Plan 9, when `argv[0]` names no registered command it retries
with `argv[1]`, which is how `./bb ls` works and how a Unix shellbang reaches
a command when `argv[0]` is the busybox itself. The first of those calls
returns `ErrNotRegistered` having run nothing, so a guard on entry to `Run`
would break dispatch, while a guard sited after a command has been selected
does not.

Plan 9 is the exception rather than the reason. It does not take the `argv[1]`
retry at all -- `main.go:109` guards it with `runtime.GOOS != "plan9"` -- and
reaches a shellbang's command by shifting `os.Args` to `os.Args[2:]` at
`main.go:101`, before the single call to `Run`. `GLOSSARY.md` records the same
thing under **Dispatch**. The guard is sited where it is for the Unix retry,
and Plan 9 is simply the platform that never needs a second call.

## Consequences

- Running a second command in one process is a programming error, refused by
  `bbmain.Run` rather than left to misbehave. The refusal must not disturb the
  `argv[1]` retry, which is dispatch still choosing its first command.

- An in-process dispatcher is now a breaking change rather than a feature
  someone can add later. A shell wanting to run a busybox command without
  forking must fork and exec, or this decision has to be revisited and 0004
  revisited with it. This is the real cost of the decision and the reason it
  is recorded separately.

- A command need not restore package-level state it mutates, because nothing
  else in the process will observe it afterwards. This was already true in
  practice; it is now something the project promises.

- The invariant is cheap to check and expensive to recover if it lapses
  silently, which is why it is enforced in code rather than only documented
  here.
