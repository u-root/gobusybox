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

// lazyInitArgs are the extra makebb arguments that build the candidate
// busybox -- the one whose behaviour has to match the baseline's.
//
// It is empty, because the flag it will hold does not exist yet. Deferring a
// library's init to the command that needs it lands as -lazy-init, off by
// default, and it is step 4 of that work which first produces a busybox
// differing from its baseline. Until then both builds are made the same way
// and this test compares a build against itself.
//
// That is the point of landing it now. The failure mode the rewrite
// introduces is a library whose init silently stops running, which produces
// no failure until something reads state that was never set, and whose blast
// radius is every command importing that library rather than the one command
// being rewritten. The check for it has to exist, and has to have been shown
// to fail when it should, before there is anything for it to catch. Turning
// it on is then a one-line change to a test that is already trusted.
//
// For that to mean anything, some fixture's libraries have to have
// initialization whose effect reaches the command's output. Only libinit
// does; see compareInitTrace for why the init trace cannot stand in for it.
var lazyInitArgs []string

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
// init at program start.
//
// It demands they be identical, which holds while both builds are made the
// same way. Step 4 of the lazy init work makes them differ on purpose: a
// deferred library init no longer runs at program start, so the candidate's
// list becomes a subset of the baseline's, and this is the one place that
// then has to be relaxed.
//
// Relaxing it to allow a subset is worth less than it looks, and must not be
// mistaken for the guard. GODEBUG=inittrace=1 reports what the runtime ran
// before main, so a library init that has become a function the command calls
// is absent from the trace whether the command called it or silently skipped
// it. A subset check cannot tell those apart, and the silent skip is the
// failure this whole test exists to catch.
//
// What catches it is the output comparison, and only if some library's
// initialization reaches the output. That is what the libinit fixture is for.
// The trace is worth comparing anyway, because a package appearing that the
// baseline never ran means the rewrite added initialization rather than
// deferring it, which no output check would notice.
func compareInitTrace(t *testing.T, cmdName string, baseline, candidate commandRun) {
	t.Helper()

	if !slices.Equal(baseline.initPackages, candidate.initPackages) {
		t.Errorf("Packages running init for %s differ between the two builds:\n%s",
			cmdName, diffPackages(baseline.initPackages, candidate.initPackages))
	}
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
			candidate := buildBusybox(t, mkbb, dir, "bb-lazy", tt, lazyInitArgs)

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
	candidate := build("bb-lazy", lazyInitArgs)

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
