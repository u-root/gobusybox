// Copyright 2015-2024 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package golang is an API to the Go compiler.
package golang

import "testing"

// TestUrootUsage makes sure that no changes break u-root.
func TestUrootUsage(t *testing.T) {
	_ = Default().GoCmd("tool", "doc", "fmt")
}
