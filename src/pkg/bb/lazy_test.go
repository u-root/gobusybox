// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bb

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"

	"github.com/u-root/gobusybox/src/pkg/bb/bbinternal"
)

// recorder keeps what a build reported, which is the only place a package a
// pattern could not move is named.
type recorder struct {
	lines []string
}

func (r *recorder) Printf(format string, v ...any) {
	r.lines = append(r.lines, fmt.Sprintf(format, v...))
}

func (r *recorder) Print(v ...any) {
	r.lines = append(r.lines, fmt.Sprint(v...))
}

func (r *recorder) text() string {
	return strings.Join(r.lines, "\n")
}

// lib is a library package with nothing in it that refuses laziness.
func lib(path string) *packages.Package {
	return &packages.Package{
		PkgPath: path,
		Name:    path,
		Imports: map[string]*packages.Package{},
		Module:  &packages.Module{Path: path},
	}
}

// withAsm refuses laziness the way a real package does, by carrying an
// assembly file that can bind a symbol here from somewhere else.
func withAsm(p *packages.Package) *packages.Package {
	p.OtherFiles = append(p.OtherFiles, "empty.s")
	return p
}

// command builds the one command of a busybox importing deps, as planInit
// takes it.
func command(path string, deps ...*packages.Package) []*bbinternal.Package {
	cmd := lib(path)
	for _, d := range deps {
		cmd.Imports[d.PkgPath] = d
	}
	return []*bbinternal.Package{{Pkg: cmd}}
}

// An exact path is a claim about one package, so a build that cannot keep it
// fails. This is step 4's rule and step 5 does not touch it.
func TestAnExactPathThatCannotBeDeferredFailsTheBuild(t *testing.T) {
	for _, tt := range []struct {
		desc string
		path string
		want string
	}{
		{"a path that is not here", "example.com/m/pkg/absent", "is not imported by any command in this busybox"},
		{"a command", "example.com/m/cmd/one", "is a command"},
		{"a package that refuses", "example.com/m/pkg/asm", "has an assembly file"},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			cmds := command("example.com/m/cmd/one", withAsm(lib("example.com/m/pkg/asm")), lib("example.com/m/pkg/ok"))

			r := &recorder{}
			_, err := planInit(r, cmds, []string{tt.path})
			if err == nil {
				t.Fatalf("planInit(%q) succeeded, want an error", tt.path)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to hold %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), tt.path) {
				t.Errorf("error = %q, want it to name %q", err, tt.path)
			}
		})
	}
}

// A pattern is a request for whatever a subtree can give, so one package
// refusing does not fail the build. It is reported instead.
func TestAPatternDefersWhatItCanAndReportsTheRest(t *testing.T) {
	cmds := command("example.com/m/cmd/one", withAsm(lib("example.com/m/pkg/asm")), lib("example.com/m/pkg/ok"))

	r := &recorder{}
	plan, err := planInit(r, cmds, []string{"example.com/m/pkg/..."})
	if err != nil {
		t.Fatalf("planInit: %v", err)
	}
	if !plan.Deferred("example.com/m/pkg/ok") {
		t.Error("the package that could move did not")
	}
	if plan.Deferred("example.com/m/pkg/asm") {
		t.Error("the package with an assembly file moved")
	}

	report := r.text()
	if !strings.Contains(report, "example.com/m/pkg/asm") {
		t.Errorf("report = %q, want it to name the package left behind", report)
	}
	if !strings.Contains(report, "has an assembly file") {
		t.Errorf("report = %q, want it to give the reason", report)
	}
	if strings.Contains(report, "example.com/m/pkg/ok") {
		t.Errorf("report = %q, want it to leave out the package that moved", report)
	}
}

// Leniency stops at doing nothing. A pattern that moved no package is the
// same silent no-op an exact path is refused for.
func TestAPatternThatMovesNothingFailsTheBuild(t *testing.T) {
	for _, tt := range []struct {
		desc    string
		pattern string
		want    string
	}{
		{"a subtree that is not here", "example.com/elsewhere/...", "matches no library in this busybox"},
		{"a subtree holding only commands", "example.com/m/cmd/...", "matches no library in this busybox"},
		{"a subtree whose packages all refuse", "example.com/m/pkg/...", "can defer none of them"},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			cmds := command("example.com/m/cmd/one", withAsm(lib("example.com/m/pkg/asm")))

			r := &recorder{}
			_, err := planInit(r, cmds, []string{tt.pattern})
			if err == nil {
				t.Fatalf("planInit(%q) succeeded, want an error", tt.pattern)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to hold %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), tt.pattern) {
				t.Errorf("error = %q, want it to name the pattern %q", err, tt.pattern)
			}
		})
	}
}

// The refusal says which packages the pattern found as well as that it found
// none it could move, because the next question is always which ones.
func TestAPatternThatMovesNothingNamesWhatItFound(t *testing.T) {
	cmds := command("example.com/m/cmd/one", withAsm(lib("example.com/m/pkg/asm")))

	r := &recorder{}
	_, err := planInit(r, cmds, []string{"example.com/m/pkg/..."})
	if err == nil {
		t.Fatal("planInit succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "example.com/m/pkg/asm has an assembly file") {
		t.Errorf("error = %q, want it to name the package and its reason", err)
	}
}

// A pattern's packages are listed under it, because the refusals are sorted
// and a package's line would otherwise sort away from the pattern that found
// it.
//
// A trailing /... matching its own subtree root is the case that always
// misorders: "pkg has" sorts before "pkg/... matches", which sorts before
// "pkg/sub has", so the summary lands between its own two packages.
func TestARefusedPatternKeepsItsPackagesUnderIt(t *testing.T) {
	cmds := command("example.com/m/cmd/one",
		withAsm(lib("example.com/m/pkg")),
		withAsm(lib("example.com/m/pkg/sub")))

	r := &recorder{}
	_, err := planInit(r, cmds, []string{"example.com/m/pkg/..."})
	if err == nil {
		t.Fatal("planInit succeeded, want an error")
	}
	want := "cannot defer the initialization of:\n" +
		"\texample.com/m/pkg/... matches 2 library(s) and can defer none of them\n" +
		"\t\texample.com/m/pkg has an assembly file\n" +
		"\t\texample.com/m/pkg/sub has an assembly file"
	if got := err.Error(); got != want {
		t.Errorf("error is\n%s\nwant\n%s", got, want)
	}
}

// One pattern given twice is refused once. The refusals are what the build
// failed on, and the same text twice reads like two separate problems.
func TestOnePatternGivenTwiceIsRefusedOnce(t *testing.T) {
	cmds := command("example.com/m/cmd/one", withAsm(lib("example.com/m/pkg/asm")))

	r := &recorder{}
	_, err := planInit(r, cmds, []string{"example.com/m/pkg/...", "example.com/m/pkg/..."})
	if err == nil {
		t.Fatal("planInit succeeded, want an error")
	}
	if got := strings.Count(err.Error(), "can defer none of them"); got != 1 {
		t.Errorf("the refusal appears %d times in\n%s\nwant 1", got, err)
	}
}

// Nothing named is the ordinary build, which has to stay exactly as it was.
func TestNoPatternsPlansNothing(t *testing.T) {
	cmds := command("example.com/m/cmd/one", lib("example.com/m/pkg/ok"))

	r := &recorder{}
	plan, err := planInit(r, cmds, nil)
	if err != nil {
		t.Fatalf("planInit: %v", err)
	}
	if plan != nil {
		t.Errorf("plan = %v, want nil", plan)
	}
	if len(r.lines) != 0 {
		t.Errorf("report = %q, want silence", r.text())
	}
}

// A build that moved everything it was asked to has nothing to report, and
// saying so anyway would train the reader to skip the line.
func TestAPatternWithNothingLeftBehindReportsNothing(t *testing.T) {
	cmds := command("example.com/m/cmd/one", lib("example.com/m/pkg/ok"))

	r := &recorder{}
	if _, err := planInit(r, cmds, []string{"example.com/m/pkg/..."}); err != nil {
		t.Fatalf("planInit: %v", err)
	}
	if len(r.lines) != 0 {
		t.Errorf("report = %q, want silence", r.text())
	}
}

// Two patterns reaching one refused package report it once. The report is
// read by a person, and the same line twice reads like two packages.
func TestTwoPatternsReportARefusalOnce(t *testing.T) {
	cmds := command("example.com/m/cmd/one", withAsm(lib("example.com/m/pkg/asm")), lib("example.com/m/pkg/ok"))

	r := &recorder{}
	if _, err := planInit(r, cmds, []string{"example.com/m/pkg/...", "example.com/..."}); err != nil {
		t.Fatalf("planInit: %v", err)
	}
	if got := strings.Count(r.text(), "example.com/m/pkg/asm has"); got != 1 {
		t.Errorf("the refusal appears %d times in %q, want 1", got, r.text())
	}
}
