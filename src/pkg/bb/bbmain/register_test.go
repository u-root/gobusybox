// Copyright 2018 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bbmain

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// reachedMain is what a registered main panics with to hand control back to
// the test. Run ends in os.Exit(0), so a command whose main returns normally
// would take the test process with it; panicking is the only way out that
// still proves the main was reached.
type reachedMain string

// catch runs f and returns the value it panicked with, or nil.
func catch(f func()) (panicked any) {
	defer func() { panicked = recover() }()
	f()
	return
}

// dispatch calls Run(name) and reports both of the ways it can answer: the
// value something panicked with, or the error it returned.
func dispatch(name string) (panicked any, err error) {
	panicked = catch(func() { err = Run(name) })
	return
}

// record returns a main that notes it ran and then escapes, per reachedMain.
func record(ran *[]string, name string) func() {
	return func() {
		*ran = append(*ran, name)
		panic(reachedMain(name))
	}
}

// fresh resets the package state to what a busybox process starts with, since
// the registry and the dispatch record both outlive a single test.
func fresh(t *testing.T) {
	t.Helper()
	reset := func() {
		bbCmds = map[string]bbCmd{}
		defaultCmd = nil
		dispatched, dispatchedName = false, ""
		lazyInit = false
	}
	reset()
	t.Cleanup(reset)
}

// TestRunRefusesSecondDispatchWhenInitIsLazy is ADR 0003's invariant: exactly
// one command runs per busybox process. With a library's init deferred to the
// command that needs it, a second command would run init that Go had already
// run before the first command's main, so the second is refused outright.
func TestRunRefusesSecondDispatchWhenInitIsLazy(t *testing.T) {
	fresh(t)
	lazyInit = true

	var ran []string
	var refusal any
	Register("first", Noop, func() {
		ran = append(ran, "first")
		refusal = catch(func() { _ = Run("second") })
		panic(reachedMain("first"))
	})
	Register("second", Noop, record(&ran, "second"))

	if panicked, _ := dispatch("first"); panicked != reachedMain("first") {
		t.Fatalf("dispatch(first) = %v, want it to reach first's main", panicked)
	}

	if want := []string{"first"}; !slices.Equal(ran, want) {
		t.Errorf("mains that ran = %v, want %v", ran, want)
	}
	msg, ok := refusal.(string)
	if !ok {
		t.Fatalf("second dispatch = %T(%v), want a refusal", refusal, refusal)
	}
	// The message has to name both commands, because the one that is wrong is
	// whichever of them the reader did not expect to be there.
	for _, name := range []string{"first", "second"} {
		if !strings.Contains(msg, name) {
			t.Errorf("refusal %q does not name %q", msg, name)
		}
	}
}

// TestRunAllowsRetryAfterNotRegistered guards the siting of the refusal. The
// generated main program calls Run twice on purpose: when argv[0] names no
// registered command it retries with argv[1], which is how "./bb ls" works.
// The first of those calls dispatches nothing, so a refusal at the entry to
// Run rather than after a command is selected would break dispatch itself.
func TestRunAllowsRetryAfterNotRegistered(t *testing.T) {
	fresh(t)
	lazyInit = true

	var ran []string
	Register("ls", Noop, record(&ran, "ls"))

	panicked, err := dispatch("bb")
	if !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("dispatch(bb) = %v, want %v", err, ErrNotRegistered)
	}
	if panicked != nil {
		t.Fatalf("dispatch(bb) = %v, want no refusal: a failed lookup runs nothing", panicked)
	}

	if panicked, _ := dispatch("ls"); panicked != reachedMain("ls") {
		t.Fatalf("retry dispatch(ls) = %v, want it to reach ls's main", panicked)
	}
	if want := []string{"ls"}; !slices.Equal(ran, want) {
		t.Errorf("mains that ran = %v, want %v", ran, want)
	}
}

// TestRunAllowsSecondDispatchWhenInitIsEager pins the gate. Without deferred
// init -- which is every busybox built without -lazy-init, so every busybox
// today -- each package is initialized before main whatever happens
// afterwards, so a second dispatch is exactly what Go would have done. ADR
// 0003's refusal is only load-bearing once 0004 defers that work, so it stays
// out of the way until then.
func TestRunAllowsSecondDispatchWhenInitIsEager(t *testing.T) {
	fresh(t)

	var ran []string
	var second any
	Register("first", Noop, func() {
		ran = append(ran, "first")
		second = catch(func() { _ = Run("second") })
		panic(reachedMain("first"))
	})
	Register("second", Noop, record(&ran, "second"))

	if panicked, _ := dispatch("first"); panicked != reachedMain("first") {
		t.Fatalf("dispatch(first) = %v, want it to reach first's main", panicked)
	}
	if second != reachedMain("second") {
		t.Fatalf("second dispatch = %v, want it to reach second's main", second)
	}
	if want := []string{"first", "second"}; !slices.Equal(ran, want) {
		t.Errorf("mains that ran = %v, want %v", ran, want)
	}
}
