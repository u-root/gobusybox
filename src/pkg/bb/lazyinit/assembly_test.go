// Copyright 2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lazyinit

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// asmFile writes one assembly file and returns the OtherFiles list naming it.
func asmFile(t *testing.T, src string) []string {
	t.Helper()

	file := filepath.Join(t.TempDir(), "asm.s")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatalf("Could not write the assembly fixture: %v", err)
	}
	return []string{file}
}

// The rule is that assembly refuses when it can reach a package's state
// without an import edge, and not otherwise. These are the shapes read out of
// a u-root busybox, plus the ones that discriminate between them.
func TestAssemblyRefusesOnlyWhatReachesPackageState(t *testing.T) {
	for _, tt := range []struct {
		desc string
		src  string
		want string
	}{
		{
			desc: "implementing a Go function is reached by calling it",
			src:  "#include \"textflag.h\"\n\nTEXT \u00b7addrOfStart(SB), NOSPLIT, $0-8\n\tMOVQ\t$0, ret+0(FP)\n\tRET\n",
			want: "",
		},
		{
			desc: "file-static symbols are unreachable by name",
			src:  "TEXT trampoline_start(SB),NOSPLIT,$0\n\tLEAQ stack_top(SB), AX\n\tRET\nTEXT stack_top(SB),$0\n\tLONG $0\n",
			want: "",
		},
		{
			desc: "calling a function this file defines is fine",
			src:  "TEXT \u00b7a(SB),$0\n\tCALL \u00b7b(SB)\n\tRET\nTEXT \u00b7b(SB),$0\n\tRET\n",
			want: "",
		},
		{
			desc: "data this file declares and fills is fine",
			src:  "GLOBL \u00b7table(SB),$8\nDATA \u00b7table+0(SB)/8, $1\nTEXT \u00b7get(SB),$0\n\tMOVQ \u00b7table(SB), AX\n\tRET\n",
			want: "",
		},

		{
			desc: "reading a Go symbol this file does not define refuses",
			src:  "TEXT \u00b7sum(SB),$0\n\tMOVQ \u00b7primes+0(SB), AX\n\tRET\n",
			want: "has an assembly file reading " + middleDot + "primes",
		},
		{
			desc: "naming another package refuses",
			src:  "TEXT \u00b7copy(SB),$0\n\tJMP runtime" + middleDot + "memmove(SB)\n",
			want: "has an assembly file naming runtime" + middleDot + "memmove",
		},
		{
			desc: "naming a package by its full path refuses",
			src:  "TEXT \u00b7f(SB),$0\n\tCALL " + asmSpelling("github.com/other/x.Helper") + "(SB)\n\tRET\n",
			want: "has an assembly file naming " + asmSpelling("github.com/other/x.Helper"),
		},

		{
			desc: "a #define could hide a reference, so it refuses",
			src:  "#define a0 R8\n\nTEXT \u00b7f(SB),$0\n\tRET\n",
			want: "has an assembly file using #define",
		},
		{
			desc: "a header the toolchain does not supply refuses",
			src:  "#include \"curve_amd64.h\"\n\nTEXT \u00b7f(SB),$0\n\tRET\n",
			want: "has an assembly file including curve_amd64.h",
		},
		{
			desc: "a toolchain header is known not to hide one",
			src:  "#include \"funcdata.h\"\n#include \"textflag.h\"\n\nTEXT \u00b7f(SB),NOSPLIT,$0\n\tRET\n",
			want: "",
		},
		{
			desc: "a toolchain header named outright refuses nothing",
			src:  "#include \"textflag.h\"\n\nTEXT \u00b7f(SB),$0\n\tRET\n",
			want: "",
		},

		{
			desc: "a reference inside a line comment does not count",
			src:  "TEXT \u00b7f(SB),$0\n\t// MOVQ " + middleDot + "V(SB), AX\n\tRET\n",
			want: "",
		},
		{
			desc: "a reference inside a block comment does not count",
			src:  "/* MOVQ " + middleDot + "V(SB), AX */\nTEXT \u00b7f(SB),$0\n\tRET\n",
			want: "",
		},
		{
			desc: "an empty file reaches nothing",
			src:  "",
			want: "",
		},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			got, _ := asmScan(asmFile(t, tt.src))
			if got != tt.want {
				t.Errorf("asmScan = %q, want %q", got, tt.want)
			}
		})
	}
}

// Assembly this cannot read keeps the package eager. An unreadable file has
// to cost laziness rather than correctness.
func TestUnreadableAssemblyRefuses(t *testing.T) {
	got, _ := asmScan([]string{filepath.Join(t.TempDir(), "absent.s")})
	if !strings.Contains(got, "could not be read") {
		t.Errorf("asmScan of a file that is not there = %q, want it to say so", got)
	}
}

// Only .s is read. A .syso is refused by scan before this is reached, and
// anything else in OtherFiles is not assembly at all.
func TestAssemblyScanIgnoresFilesThatAreNotAssembly(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, []byte("MOVQ "+middleDot+"V(SB), AX"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := asmScan([]string{other}); got != "" {
		t.Errorf("asmScan of a .txt = %q, want it ignored", got)
	}
}

// The symbol an assembly file names is returned so the caller can resolve it
// and make its package eager. Refusing only the package holding the assembly
// would move the hazard rather than close it.
//
// The symbol comes back with the assembler's spelling undone, because that is
// the form //go:linkname uses and linknameTarget resolves. A scanner that
// kept the assembler's runes, or that split at the first middle dot, would
// hand back "example" or "victim" -- neither of which is a package, so the
// target would be silently dropped and left deferred.
func TestAssemblyNamingAnotherPackageReportsTheWholeSymbol(t *testing.T) {
	for _, tt := range []struct {
		desc, sym, want string
	}{
		{"a domain in the path", "example.com/m/victim.Ready", "example.com/m/victim.Ready"},
		{"no dot in the first element", "nodot/victim.Ready", "nodot/victim.Ready"},
		{"a standard library package", "math/rand.Int", "math/rand.Int"},
		{"a method, which carries its receiver", "example.com/m/victim.T.Ready", "example.com/m/victim.T.Ready"},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			_, syms := asmScan(asmFile(t, "TEXT \u00b7f(SB),$0\n\tCALL "+asmSpelling(tt.sym)+"(SB)\n\tRET\n"))
			if !slices.Contains(syms, tt.want) {
				t.Errorf("symbols = %v, want %q", syms, tt.want)
			}
		})
	}
}

// asmSpelling writes a Go symbol the way the assembler requires. ASCII ".",
// "/" and "-" are not identifier characters there, so a fixture using them is
// a file the toolchain would reject.
func asmSpelling(name string) string {
	return strings.NewReplacer(".", "\u00b7", "/", "\u2215").Replace(name)
}

// A local definition keeps its receiver, so assembly defining a method does
// not look like a read of an undefined symbol.
func TestAssemblyDefiningAMethodIsSelfContained(t *testing.T) {
	got, _ := asmScan(asmFile(t, "TEXT \u00b7T\u00b7ValMethod(SB),$0\n\tRET\n"))
	if got != "" {
		t.Errorf("asmScan of a method definition = %q, want it accepted", got)
	}
}

// An include is trusted only when it names a toolchain header outright. A
// relative path is a file this has not read, and taking its base would trust
// "../../textflag.h" because it ends in a name on the list.
func TestAssemblyIncludeOfARelativePathRefuses(t *testing.T) {
	for _, inc := range []string{"../../textflag.h", "local/textflag.h"} {
		got, _ := asmScan(asmFile(t, "#include \""+inc+"\"\nTEXT \u00b7f(SB),$0\n\tRET\n"))
		if got == "" {
			t.Errorf("asmScan of #include %q was accepted, want a refusal", inc)
		}
	}
}

// The preprocessor allows space after the "#" and needs none before the
// quote, so a scanner anchored on "#define " or "#include " misses both.
func TestAssemblyPreprocessorSpellingsDoNotSlipPast(t *testing.T) {
	for _, tt := range []struct{ desc, src string }{
		{"space after the hash of define", "# define M 1\nTEXT \u00b7f(SB),$0\n\tRET\n"},
		{"space after the hash of include", "# include \"bad.h\"\nTEXT \u00b7f(SB),$0\n\tRET\n"},
		{"no space before the quote", "#include\"bad.h\"\nTEXT \u00b7f(SB),$0\n\tRET\n"},
	} {
		t.Run(tt.desc, func(t *testing.T) {
			if got, _ := asmScan(asmFile(t, tt.src)); got == "" {
				t.Errorf("asmScan of %q was accepted, want a refusal", tt.src)
			}
		})
	}
}

// Comments are stripped in one pass. Taking block comments first lets a "/*"
// inside one line comment pair with a "*/" inside a later one and delete
// every instruction between them, which hides whatever they did.
func TestACommentedSlashStarDoesNotSwallowTheFile(t *testing.T) {
	src := "// opens /* here\nTEXT \u00b7f(SB),$0\n\tMOVQ " + middleDot + "V(SB), AX\n\tRET\n// closes */ here\n"
	got, _ := asmScan(asmFile(t, src))
	if got == "" {
		t.Error("asmScan read nothing between two line comments holding /* and */, want the read of " + middleDot + "V to be seen")
	}
}
