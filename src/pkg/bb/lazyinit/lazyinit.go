// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package lazyinit decides which of a busybox's packages may have their
// initialization deferred to the command that imports them, and in what order
// the deferred ones must run.
//
// It decides only. Nothing here rewrites source or writes a file; the result
// is the input to that work. See docs/adr/0004-library-init-runs-for-the-command-that-needs-it.md.
package lazyinit

import (
	"path"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Reasons laziness is refused, as they appear in the build's report. Phrased
// to name what was found rather than what it implies, because the reader is
// looking for it in their own source.
const (
	refusedAssembly = "has an assembly file"
	refusedLinkname = "has a //go:linkname directive"
)

// scanResult is what one package's own source says about deferring its
// initialization.
type scanResult struct {
	// refusal names what was found that forbids deferring this package, or
	// is empty if nothing was.
	refusal string
	// linknameSymbols are the remote symbols named by two-argument
	// //go:linkname directives in this package, verbatim. They are edges the
	// import graph does not have, so they are collected even though this
	// package is already refused: it is the *target* that the closure cannot
	// otherwise reach. Plan.linknameTarget turns a symbol into the package
	// that holds it.
	linknameSymbols []string
}

// scan reads one package's files for the two things that let another package
// reach this one without importing it.
//
// Assembly is read from OtherFiles and directives from Syntax, both of which
// go/packages has already filtered by build constraints. That filtering is the
// point: a directive in a file excluded from this build cannot bind anything
// in it, and counting it would make packages eager on every platform for the
// sake of one.
func scan(p *packages.Package) scanResult {
	var r scanResult

	for _, f := range p.OtherFiles {
		if path.Ext(f) == ".s" {
			r.refusal = refusedAssembly
			break
		}
	}

	for _, f := range p.Syntax {
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				symbol, ok := linknameSymbol(c.Text)
				if !ok {
					continue
				}
				r.refusal = refusedLinkname
				if symbol != "" && !slices.Contains(r.linknameSymbols, symbol) {
					r.linknameSymbols = append(r.linknameSymbols, symbol)
				}
			}
		}
	}
	return r
}

// linknameSymbol reports whether a comment is a //go:linkname directive, and
// returns the remote symbol it names if it is the two-argument form.
//
// The directive is only a directive when it is the whole of the comment, so
// prose mentioning it is not one.
//
// The symbol is returned as written. Splitting it into a package and a name
// cannot be done here: both halves may contain dots, and which dot separates
// them is decided by what packages exist, not by the text. A method's symbol
// carries its receiver -- example.com/victim.(*T).Ready -- so the final dot
// is the wrong one, and an import path contains dots of its own, so the first
// one is too. Plan.linknameTarget does it against the loaded package paths.
func linknameSymbol(text string) (string, bool) {
	if !strings.HasPrefix(text, "//go:linkname") {
		return "", false
	}
	fields := strings.Fields(text)
	if len(fields) == 0 || fields[0] != "//go:linkname" {
		return "", false
	}
	if len(fields) < 3 {
		return "", true
	}
	return fields[2], true
}
