// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lazyinit

// eagerNotNamed is why a package that could have been deferred is not.
const eagerNotNamed = "is not named by -lazy-init"

// Option narrows which of a busybox's packages a Plan defers.
type Option func(*Plan)

// DeferOnly defers the packages named by paths and the packages that import
// them, and nothing else.
//
// Without it a Plan defers everything it can, which is the question "what
// could this busybox recover". DeferOnly is the answer to the different
// question a build asks: which packages is this build moving. The patterns
// are the dial, and a package named by them gives up its package-level
// variables and its init together.
//
// Paths are whole import paths. Reaching a subtree is step 5 of #165 and is
// deliberately not here: the point of the first landing is that the blast
// radius is one package of our choosing.
//
// A path that names no package in this busybox, or one that is eager for a
// reason of its own, is ignored here rather than reported. Deferred says
// which it was, so the caller can refuse a build whose -lazy-init did
// nothing; a silent no-op is exactly the failure this work has to avoid.
//
// # Why the importers come too
//
// An eager package initializes before any deferred one, so it may not import
// one. Naming a package therefore defers every package that can reach it, not
// just the one named. That closure can never meet a package that has to stay
// eager: the eager set New computes is already closed downward, so a package
// importing a deferrable one is deferrable itself.
func DeferOnly(paths []string) Option {
	return func(p *Plan) {
		deferred := map[string]bool{}
		importers := p.importers()
		for _, path := range paths {
			if !p.Deferred(path) {
				continue
			}
			markImporters(path, importers, deferred)
		}

		for path := range p.pkgs {
			// A command is not a library and is rewritten whatever
			// the patterns say, so it is never eager. Nothing
			// imports a main package, so leaving commands out
			// cannot leave an eager package importing a lazy one.
			if deferred[path] || p.Eager(path) || p.isCommand(path) {
				continue
			}
			p.eager[path] = eagerNotNamed
		}
	}
}

// importers maps each package's import path to the paths importing it
// directly, which is the import graph with every edge reversed.
func (p *Plan) importers() map[string][]string {
	rev := map[string][]string{}
	for path, pkg := range p.pkgs {
		for _, imp := range sortedImports(pkg) {
			rev[imp] = append(rev[imp], path)
		}
	}
	return rev
}

// markImporters records path and everything that can reach it through
// imports.
func markImporters(path string, importers map[string][]string, seen map[string]bool) {
	if seen[path] {
		return
	}
	seen[path] = true
	for _, importer := range importers[path] {
		markImporters(importer, importers, seen)
	}
}

// isCommand reports whether this import path is one of the busybox's
// commands.
func (p *Plan) isCommand(pkgPath string) bool {
	for _, cmd := range p.cmds {
		if cmd == pkgPath {
			return true
		}
	}
	return false
}

// Deferred reports whether the package at this import path is in the busybox
// and initializes when a command that imports it is dispatched, rather than
// at program start.
//
// It is not the negation of Eager: a path naming no package in this busybox
// is neither. EagerReason tells the two apart, being empty only for the
// package that is not here at all.
func (p *Plan) Deferred(pkgPath string) bool {
	_, known := p.pkgs[pkgPath]
	return known && !p.Eager(pkgPath)
}
