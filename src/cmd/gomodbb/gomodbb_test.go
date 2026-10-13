// Copyright 2015-2026 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParsePattern(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   string
		want pattern
		// spec is what `go get` should be given, or "" when the
		// pattern is an exclusion and must not be fetched at all.
		spec string
		// cmd is what makebb should be given.
		cmd string
	}{
		{
			name: "no version query leaves the choice to go get",
			in:   "github.com/u-root/u-root/cmds/core/...",
			want: pattern{path: "github.com/u-root/u-root/cmds/core/..."},
			spec: "github.com/u-root/u-root/cmds/core/...",
			cmd:  "github.com/u-root/u-root/cmds/core/...",
		},
		{
			name: "branch query",
			in:   "github.com/u-root/u-root/cmds/core/...@main",
			want: pattern{path: "github.com/u-root/u-root/cmds/core/...", query: "main"},
			spec: "github.com/u-root/u-root/cmds/core/...@main",
			cmd:  "github.com/u-root/u-root/cmds/core/...",
		},
		{
			name: "tag query",
			in:   "github.com/u-root/u-root/cmds/core/ls@v0.16.0",
			want: pattern{path: "github.com/u-root/u-root/cmds/core/ls", query: "v0.16.0"},
			spec: "github.com/u-root/u-root/cmds/core/ls@v0.16.0",
			cmd:  "github.com/u-root/u-root/cmds/core/ls",
		},
		{
			// gopkg.in spells a major version ".vN" rather than
			// "/vN", so the path itself can end in something that
			// looks like a version. Splitting on the last "@"
			// keeps that out of the query.
			name: "path carrying a major version suffix",
			in:   "gopkg.in/yaml.v2/cmd/foo@v2.4.0",
			want: pattern{path: "gopkg.in/yaml.v2/cmd/foo", query: "v2.4.0"},
			spec: "gopkg.in/yaml.v2/cmd/foo@v2.4.0",
			cmd:  "gopkg.in/yaml.v2/cmd/foo",
		},
		{
			// A commit hash is just another query.
			name: "revision query",
			in:   "github.com/u-root/u-root/cmds/core/ls@98a84dad0e9c",
			want: pattern{path: "github.com/u-root/u-root/cmds/core/ls", query: "98a84dad0e9c"},
			spec: "github.com/u-root/u-root/cmds/core/ls@98a84dad0e9c",
			cmd:  "github.com/u-root/u-root/cmds/core/ls",
		},
		{
			// An exclusion names commands the other patterns
			// already fetched, so it is never fetched itself, but
			// makebb still has to see the leading "-".
			name: "exclusion",
			in:   "-github.com/u-root/u-root/cmds/core/ip",
			want: pattern{path: "github.com/u-root/u-root/cmds/core/ip", exclude: true},
			spec: "github.com/u-root/u-root/cmds/core/ip",
			cmd:  "-github.com/u-root/u-root/cmds/core/ip",
		},
		{
			name: "exclusion with a version query",
			in:   "-github.com/u-root/u-root/cmds/core/ip@main",
			want: pattern{path: "github.com/u-root/u-root/cmds/core/ip", query: "main", exclude: true},
			spec: "github.com/u-root/u-root/cmds/core/ip@main",
			cmd:  "-github.com/u-root/u-root/cmds/core/ip",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePattern(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parsePattern(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
			if got := got.fetchSpec(); got != tt.spec {
				t.Errorf("parsePattern(%q).fetchSpec() = %q, want %q", tt.in, got, tt.spec)
			}
			if got := parsePattern(tt.in).commandPath(); got != tt.cmd {
				t.Errorf("parsePattern(%q).commandPath() = %q, want %q", tt.in, got, tt.cmd)
			}
		})
	}
}

// The version query decides which source is fetched, and the go.mod the fetch
// writes decides it from then on. Handing makebb the query as well would be a
// second answer to a settled question, and `go list` rejects it outright:
//
//	malformed import path ".../cmds/core/ls@main": invalid char '@'
func TestCommandPathDropsTheVersionQuery(t *testing.T) {
	for _, in := range []string{
		"github.com/u-root/u-root/cmds/core/...@main",
		"-github.com/u-root/u-root/cmds/core/ip@v0.16.0",
	} {
		if got := parsePattern(in).commandPath(); strings.Contains(got, "@") {
			t.Errorf("parsePattern(%q).commandPath() = %q, want no version query", in, got)
		}
	}
}
