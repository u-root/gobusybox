// Copyright 2015-2024 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package golang

import "testing"

func TestCompareGoVersions(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want int
	}{
		{"1.21", "1.21", 0},
		{"1.21", "1.22", -1},
		{"1.22", "1.21", 1},
		// Missing components do NOT count as zero. A language version
		// sorts below its releases, so "1.26" is lower than both
		// "1.26.0" and "1.26.6". Treating them as equal is what left a
		// generated go.work directive too low for a member module.
		{"1.26", "1.26.6", -1},
		{"1.26.6", "1.26", 1},
		{"1.26", "1.26.0", -1},
		{"1.26.0", "1.26", 1},
		{"1.26.5", "1.26.6", -1},
		// `go version` output compares against go.mod directives.
		{"go1.26.6", "1.26.6", 0},
		{"go1.25", "1.26", -1},
		// A pre-release sorts ABOVE the bare language version and below
		// the first release: 1.26 < 1.26rc1 < 1.26.0.
		{"1.21rc1", "1.21", 1},
		{"1.21", "1.21rc1", -1},
		{"1.26rc1", "1.26.0", -1},
		{"1.26.0", "1.26rc1", 1},
		{"1.21rc1", "1.21rc2", -1},
		{"1.21rc1", "1.21rc1", 0},
		{"1.20", "1.21rc1", -1},
		// Pre-release counters are numeric, not lexical.
		{"1.26rc2", "1.26rc10", -1},
		{"1.26rc10", "1.26rc2", 1},
		// Multi-digit components are compared numerically, not
		// lexically.
		{"1.9", "1.10", -1},
		{"1.100", "1.99", 1},
		// Invalid versions sort below valid ones, and equal to each
		// other.
		{"", "1.26", -1},
		{"devel", "1.26", -1},
		{"", "", 0},
	} {
		if got := CompareGoVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("CompareGoVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestMajorMinorGoVersion(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want string
	}{
		{"go1.22.4", "1.22"},
		{"1.22.4", "1.22"},
		{"go1.26", "1.26"},
		{"1.21rc1", "1.21"},
		{"go1.21rc1", "1.21"},
		// Unparseable input falls back.
		{"", "fallback"},
		{"go", "fallback"},
		{"devel", "fallback"},
		{"1", "fallback"},
	} {
		if got := MajorMinorGoVersion(tt.in, "fallback"); got != tt.want {
			t.Errorf("MajorMinorGoVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
