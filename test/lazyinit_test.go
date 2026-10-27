package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/u-root/gobusybox/src/pkg/golang"
)

// lazyInitFlags turns a list of import paths into the makebb arguments that
// defer them.
//
// Repeated rather than comma-separated, because that is how makebb's other
// list-valued flags read and an import path is the kind of thing someone
// pastes one of at a time.
func lazyInitFlags(paths []string) []string {
	var args []string
	for _, path := range paths {
		args = append(args, "-lazy-init", path)
	}
	return args
}

// lazyInitPaths is what the gomodbb arm defers.
//
// u-root's own pkg/ls rather than one of ls's third-party imports: it is
// reached by exactly one command, which is the shape 125 of the 228 library
// packages in the 120-command u-root busybox have, and it belongs to the
// module the build is about rather than to something vendored underneath it.
var lazyInitPaths = []string{"github.com/u-root/u-root/pkg/ls"}

// commandRun is what one command did in one busybox: what it printed, how it
// exited, and which packages ran their init before it reached its own main.
type commandRun struct {
	// output is stdout and stderr interleaved, as a user would see them.
	output string
	// exitCode is the exit status, or -1 if the command was signalled.
	exitCode int
	// initPackages names the packages that ran an init, in the order the
	// runtime ran them.
	initPackages []string
}

// buildBusybox builds one fixture into a busybox called name and returns the
// path to it. extra is appended to the fixture's own makebb arguments, and is
// what distinguishes the candidate build from the baseline.
func buildBusybox(t *testing.T, mkbb, dir, name string, f fixture, extra []string) string {
	t.Helper()

	binary := filepath.Join(dir, name)
	args := append([]string{"-o", binary}, f.extraArgs...)
	args = append(args, extra...)
	args = append(args, f.cmds...)

	t.Logf("Run: (cd %s && GO111MODULE=on %s %s)", f.wd, mkbb, strings.Join(args, " "))
	cmd := exec.Command(mkbb, args...)
	cmd.Dir = f.wd
	cmd.Env = append(os.Environ(), "GO111MODULE=on")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("makebb: %s", string(out))
		t.Fatalf("Could not build the %s busybox for %s: %v", name, f.testname, err)
	}
	return binary
}

// runCommand dispatches one command out of a busybox by argv[1] and records
// what it did.
//
// The command runs twice. The first run is what a user would see, and is
// where the output and the exit status come from. The second sets
// GODEBUG=inittrace=1, which the runtime writes to stderr -- the same place
// several fixtures write their own output -- so it is kept to a run of its
// own rather than filtered back out of a shared one.
func runCommand(t *testing.T, binary, cmdName string, args ...string) commandRun {
	t.Helper()

	argv := append([]string{cmdName}, args...)
	shown := strings.Join(argv, " ")

	t.Logf("Run: %s %s", binary, shown)
	cmd := exec.Command(binary, argv...)
	out, err := cmd.CombinedOutput()
	exitIsAResult(t, binary, shown, err)

	t.Logf("Run: GODEBUG=inittrace=1 %s %s", binary, shown)
	var stderr bytes.Buffer
	trace := exec.Command(binary, argv...)
	trace.Env = append(os.Environ(), "GODEBUG=inittrace=1")
	trace.Stdout = io.Discard
	trace.Stderr = &stderr
	exitIsAResult(t, binary, shown, trace.Run())

	return commandRun{
		output:       string(out),
		exitCode:     cmd.ProcessState.ExitCode(),
		initPackages: initTracePackages(stderr.String()),
	}
}

// exitIsAResult fails the test only when the command could not be run at all.
// A non-zero exit status is one of the things this test compares between two
// builds, not a failure in itself: a command exiting 1 in both builds agrees
// with itself, and insisting on success here would throw that comparison away
// before it was made.
func exitIsAResult(t *testing.T, binary, cmdName string, err error) {
	t.Helper()

	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("Could not run %s %s: %v", binary, cmdName, err)
	}
}

// initTracePackages names the packages that ran an init, read out of the
// lines GODEBUG=inittrace=1 makes the runtime write to stderr, one per
// package:
//
//	init internal/bytealg @0.008 ms, 0 ms clock, 0 bytes, 0 allocs
//
// Only the names are kept, in the order the runtime ran them. The timings and
// the allocation counts differ from one run to the next, so comparing those
// would report a difference every time.
func initTracePackages(stderr string) []string {
	var pkgs []string
	for line := range strings.SplitSeq(stderr, "\n") {
		// Matched on shape rather than on an "init " prefix alone, so that
		// a command writing a line of its own beginning with "init " to
		// stderr is not taken for one of the runtime's.
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "init" || !strings.HasPrefix(fields[2], "@") {
			continue
		}
		pkgs = append(pkgs, fields[1])
	}
	return pkgs
}

// compareRuns checks that one command behaved the same in both builds, on all
// three channels. Output and exit status must match whatever laziness does;
// only compareInitTrace is expected to be relaxed later.
func compareRuns(t *testing.T, cmdName string, baseline, candidate commandRun) {
	t.Helper()

	if baseline.output != candidate.output {
		t.Errorf("Output of %s differs between the two builds:\n\tbaseline  = %q\n\tcandidate = %q",
			cmdName, baseline.output, candidate.output)
	}
	if baseline.exitCode != candidate.exitCode {
		t.Errorf("Exit status of %s differs between the two builds: baseline = %d, candidate = %d",
			cmdName, baseline.exitCode, candidate.exitCode)
	}
	compareInitTrace(t, cmdName, baseline, candidate)
}

// compareInitTrace reports any difference between which packages ran their
// init at program start that deferring initialization does not explain.
//
// The candidate must be the baseline with entries removed and none added or
// moved. Deferring a library's init takes it out of the trace; nothing else
// about this rewrite may touch it.
//
// What that does *not* catch is the failure this whole test exists for.
// GODEBUG=inittrace=1 reports what the runtime ran before main, so a library
// init that has become a function the command calls is absent from the trace
// whether the command called it or silently skipped it. The two are
// indistinguishable here.
//
// What catches it is the output comparison, and only if some library's
// initialization reaches the output. That is what the libinit fixture is for,
// and TestADeferredInitLeavesTheStartupTrace is where both halves are stated
// as one assertion. The trace is worth comparing anyway, because a package
// appearing that the baseline never ran means the rewrite added
// initialization rather than deferring it, which no output check would
// notice.
func compareInitTrace(t *testing.T, cmdName string, baseline, candidate commandRun) {
	t.Helper()

	if isSubsequence(candidate.initPackages, baseline.initPackages) {
		return
	}
	t.Errorf("Packages running init for %s differ between the two builds by more than deferral:\n%s",
		cmdName, diffPackages(baseline.initPackages, candidate.initPackages))
}

// isSubsequence reports whether all of short appears in long, in that order.
//
// A subsequence rather than a subset, because deferring a package removes it
// from the trace and leaves the order of everything else alone. Asserting the
// weaker property would let a rewrite reorder the packages that still
// initialize at program start without anything noticing.
func isSubsequence(short, long []string) bool {
	i := 0
	for _, pkg := range long {
		if i < len(short) && short[i] == pkg {
			i++
		}
	}
	return i == len(short)
}

// diffPackages describes how two package lists differ, as the entries only
// one of them holds. A busybox can run well over a hundred packages' init, so
// printing both lists in full would bury the line that matters.
func diffPackages(baseline, candidate []string) string {
	only := func(a, b []string) []string {
		in := make(map[string]bool, len(b))
		for _, pkg := range b {
			in[pkg] = true
		}
		var out []string
		for _, pkg := range a {
			if !in[pkg] {
				out = append(out, pkg)
			}
		}
		return out
	}

	var d strings.Builder
	if missing := only(baseline, candidate); len(missing) > 0 {
		fmt.Fprintf(&d, "\tran only in the baseline: %s\n", strings.Join(missing, " "))
	}
	if added := only(candidate, baseline); len(added) > 0 {
		fmt.Fprintf(&d, "\tran only in the candidate: %s\n", strings.Join(added, " "))
	}
	if d.Len() == 0 {
		fmt.Fprint(&d, "\tthe same packages, in a different order\n")
	}
	return d.String()
}

// TestLazyInitDifferential builds every fixture busybox twice and checks that
// the two agree on what each command prints, on how it exits, and on which
// packages ran their init.
//
// It runs in the default suite rather than behind -external, so that it
// guards every pull request. A guard that only runs on demand is a guard
// nobody runs.
func TestLazyInitDifferential(t *testing.T) {
	mkbb := buildTool(t, "makebb")

	goVersion, err := golang.Default().Version()
	if err != nil {
		t.Fatalf("Could not determine Go version: %v", err)
	}

	for _, tt := range fixtures(t) {
		t.Run(tt.testname, func(t *testing.T) {
			skipIfUnsupported(t, goVersion, tt.unsupportedGoVersions)

			dir := t.TempDir()
			baseline := buildBusybox(t, mkbb, dir, "bb-baseline", tt, nil)
			candidate := buildBusybox(t, mkbb, dir, "bb-lazy", tt, lazyInitFlags(tt.lazyInit))

			// A fixture with no expected output is built both ways but
			// never run: nested-modules holds commands this host may not
			// be able to execute, and import-name-conflict prints a random
			// number. Building them both ways still earns its keep, since
			// a rewrite defect bad enough to break the build fails above.
			for _, cmdName := range slices.Sorted(maps.Keys(tt.want)) {
				t.Run(cmdName, func(t *testing.T) {
					compareRuns(t, cmdName,
						runCommand(t, baseline, cmdName),
						runCommand(t, candidate, cmdName))
				})
			}
		})
	}
}

// TestLazyInitDifferentialGomodbb is the same check for the other caller of
// the build engine.
//
// makebb and gomodbb share one engine -- bb.BuildBusybox, and with it the
// entire rewrite, which lives in pkg/bb and nowhere else. The fixtures above
// therefore already cover the code a deferred init would break, for both
// commands. What they do not cover is the *input* gomodbb hands that engine:
// it resolves packages inside a synthetic module, with Env.Dir set and
// GOWORK=off, over source in a read-only module cache rather than a checkout.
// makebb never does any of that, so no fixture can produce the shape. Since
// deferring a library's init is decided from the import graph, the context
// that graph is resolved in is worth a check of its own.
//
// It needs the proxy, so it sits behind -external and the default suite is
// unchanged. That is a weaker guarantee than the fixtures get, and the reason
// is gomodbb pinning GOPROXY to the public proxy: until it can be pointed at
// a warm module cache, there is no offline gomodbb to put in the default
// suite.
func TestLazyInitDifferentialGomodbb(t *testing.T) {
	if !*external {
		t.Skip("Skipping; pass -external to fetch from the module proxy and run this")
	}

	dir := t.TempDir()
	env := []string{"GOPATH=" + scratchGOPATH(t)}
	gomodbbBin := buildTool(t, "gomodbb")

	// Both builds share one fetching module. The second then resolves
	// nothing, because the go.mod the first wrote already records what the
	// patterns came to, which is what -mod-dir is for. Without it the proxy
	// would be asked the same question twice, and the two builds could in
	// principle be handed different source.
	modDir := filepath.Join(dir, "fetch")

	cmds := []string{
		"github.com/u-root/u-root/cmds/core/echo@" + urootRev,
		"github.com/u-root/u-root/cmds/core/ls@" + urootRev,
	}

	build := func(name string, extra []string) string {
		args := append([]string{"-mod-dir", modDir, "-o", name}, extra...)
		run(t, dir, env, gomodbbBin, append(args, cmds...)...)
		return filepath.Join(dir, name)
	}

	baseline := build("bb-baseline", nil)
	candidate := build("bb-lazy", lazyInitFlags(lazyInitPaths))

	// A directory of known contents, so that ls prints the same thing
	// whichever build ran it.
	lsDir := filepath.Join(dir, "lsdir")
	if err := os.MkdirAll(lsDir, 0o755); err != nil {
		t.Fatalf("Could not create a directory for ls: %v", err)
	}
	for _, name := range []string{"alpha", "beta"} {
		if err := os.WriteFile(filepath.Join(lsDir, name), nil, 0o644); err != nil {
			t.Fatalf("Could not create a file for ls: %v", err)
		}
	}

	// echo first, because its output depends on nothing outside the
	// process, so a difference is the rewrite and not the environment.
	//
	// ls as well, though, and not only because it is in the busybox: it is
	// the command here whose libraries -- pflag, go-humanize, u-root's own
	// pkg/ls -- have initialization worth deferring, and a command that is
	// built but never dispatched is one whose deferred init is never
	// called. Running only echo would leave exactly the interesting half
	// of this busybox unexercised.
	compareRuns(t, "echo",
		runCommand(t, baseline, "echo", "hello"),
		runCommand(t, candidate, "echo", "hello"))
	compareRuns(t, "ls",
		runCommand(t, baseline, "ls", lsDir),
		runCommand(t, candidate, "ls", lsDir))
}

// fixtureNamed returns the fixture with this testname, so that a test about
// one fixture reads its build configuration from the one list rather than
// repeating it and drifting from it.
func fixtureNamed(t *testing.T, name string) fixture {
	t.Helper()

	for _, f := range fixtures(t) {
		if f.testname == name {
			return f
		}
	}
	t.Fatalf("No fixture named %q", name)
	return fixture{}
}

// TestADeferredInitLeavesTheStartupTrace is the guard on -lazy-init actually
// doing something, and on it not doing too much. Both halves are needed and
// neither is a guard alone.
//
// The first half is that lonely stops initializing at program start for a
// command that never imports it. Without it, a -lazy-init that quietly
// deferred nothing would pass every other check here:
// TestLazyInitDifferential compares a candidate against a baseline, and a
// candidate identical to its baseline agrees with it on everything.
//
// The second half is that first, which does import lonely, still prints what
// lonely's initialization left behind. A deferred init disappears from the
// trace whether it was called or silently skipped, so the first half alone is
// also satisfied by a rewrite that dropped the initialization entirely --
// which is the failure with the largest blast radius this change can have.
//
// Measured before it was written, so it is known not to be vacuous:
//
//	$ makebb -o bb ./cmd/first ./cmd/second && GODEBUG=inittrace=1 ./bb second
//	init .../test/libinit/pkg/lonely @5.3 ms, 0.012 ms clock, 32 bytes, 1 allocs
//
// second never imports lonely and pays for it anyway.
func TestADeferredInitLeavesTheStartupTrace(t *testing.T) {
	const lonely = "github.com/u-root/gobusybox/test/libinit/pkg/lonely"

	f := fixtureNamed(t, "library-init")
	if !slices.Contains(f.lazyInit, lonely) {
		t.Fatalf("The library-init fixture defers %v, and this test is about %s", f.lazyInit, lonely)
	}

	mkbb := buildTool(t, "makebb")
	dir := t.TempDir()
	baseline := buildBusybox(t, mkbb, dir, "bb-baseline", f, nil)
	candidate := buildBusybox(t, mkbb, dir, "bb-lazy", f, lazyInitFlags(f.lazyInit))

	// second does not import lonely, so deferring lonely takes it out of
	// what second initializes.
	base := runCommand(t, baseline, "second")
	cand := runCommand(t, candidate, "second")
	if !slices.Contains(base.initPackages, lonely) {
		t.Errorf("second did not run %s's init even in the baseline, so deferring it proves nothing.\nPackages: %v",
			lonely, base.initPackages)
	}
	if slices.Contains(cand.initPackages, lonely) {
		t.Errorf("second still runs %s's init at program start with -lazy-init, so nothing was deferred", lonely)
	}

	// first does import lonely, so lonely's init still has to run -- now
	// as a call first makes rather than before main. Only the output can
	// tell that from its never having run.
	const ran = "lonely.init(saw lonely.var)"
	got := runCommand(t, candidate, "first")
	if !strings.Contains(got.output, ran) {
		t.Errorf("first printed %q, which does not contain %q: a deferred init must still run for a command that imports it",
			got.output, ran)
	}
	if slices.Contains(got.initPackages, lonely) {
		t.Errorf("first runs %s's init at program start with -lazy-init, so it was not deferred for the command that needs it either", lonely)
	}
}

// TestLazyInitRefusesAPackageItCannotDefer checks that naming a package that
// does not move fails the build.
//
// A silent no-op is the failure mode this work is least able to notice: every
// comparison in this file is between a candidate and a baseline, and a
// candidate that deferred nothing agrees with its baseline perfectly.
func TestLazyInitRefusesAPackageItCannotDefer(t *testing.T) {
	mkbb := buildTool(t, "makebb")
	f := fixtureNamed(t, "library-init")
	dir := t.TempDir()

	for _, tt := range []struct {
		name string
		path string
		want string
	}{
		{
			name: "a package no command imports",
			path: "github.com/u-root/gobusybox/test/libinit/pkg/nosuchthing",
			want: "is not imported by any command in this busybox",
		},
		{
			name: "the standard library",
			path: "fmt",
			want: "is a standard library package",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"-o", filepath.Join(dir, "bb-"+t.Name())}, f.extraArgs...)
			args = append(args, "-lazy-init", tt.path)
			args = append(args, f.cmds...)

			cmd := exec.Command(mkbb, args...)
			cmd.Dir = f.wd
			cmd.Env = append(os.Environ(), "GO111MODULE=on")
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("Building with -lazy-init %s succeeded, want a refusal:\n%s", tt.path, out)
			}
			if !strings.Contains(string(out), tt.want) {
				t.Errorf("Building with -lazy-init %s said:\n%s\nwant a message containing %q", tt.path, out, tt.want)
			}
		})
	}
}

// TestDeferringArmsTheOneCommandGuard checks that a busybox whose libraries
// initialize lazily is also one that refuses to dispatch twice.
//
// bbmain's refusal is gated on a flag that register.go declares and never
// sets, because register.go is copied verbatim into every generated tree and
// the refusal is only correct once initialization has been deferred: while
// every package initializes before main, a second command runs exactly the
// init Go would have run for it. Arming the flag is a generated file beside
// it, and nothing else in this suite observes whether it was written.
//
// It is checked by reading the generated tree rather than by dispatching
// twice, because the generated main program only ever calls Run a second time
// on a name it did not recognise, which returns before reaching the guard. A
// busybox that dispatches twice for real has to be written on purpose, and
// there is none here.
func TestDeferringArmsTheOneCommandGuard(t *testing.T) {
	mkbb := buildTool(t, "makebb")
	f := fixtureNamed(t, "library-init")

	generate := func(name string, extra []string) string {
		dir := filepath.Join(t.TempDir(), name)
		args := append([]string{"-g", "-gen-dir", dir}, f.extraArgs...)
		args = append(args, extra...)
		args = append(args, f.cmds...)

		t.Logf("Run: (cd %s && %s %s)", f.wd, mkbb, strings.Join(args, " "))
		cmd := exec.Command(mkbb, args...)
		cmd.Dir = f.wd
		cmd.Env = append(os.Environ(), "GO111MODULE=on")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Could not generate the %s tree: %v\n%s", name, err, out)
		}
		return dir
	}

	armed := filepath.Join(generate("lazy", lazyInitFlags(f.lazyInit)), "pkg", "bbmain", "lazyinit.go")
	src, err := os.ReadFile(armed)
	if err != nil {
		t.Fatalf("A busybox that defers initialization did not arm bbmain's guard: %v", err)
	}
	if !strings.Contains(string(src), "lazyInit = true") {
		t.Errorf("%s does not set lazyInit:\n%s", armed, src)
	}

	// And a busybox that defers nothing is left exactly as it was, so
	// that nothing about dispatch changes for builds that did not ask for
	// any of this.
	unarmed := filepath.Join(generate("baseline", nil), "pkg", "bbmain", "lazyinit.go")
	if _, err := os.Stat(unarmed); !os.IsNotExist(err) {
		t.Errorf("A busybox that defers nothing armed bbmain's guard anyway (%v)", err)
	}
}
