// Copyright 2018 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package findpkg

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestModules(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "mod1/cmd/cmd1"),
		filepath.Join(dir, "mod1/cmd/cmd2"),
		filepath.Join(dir, "mod1/nestedmod1/cmd/cmd5"),
		filepath.Join(dir, "mod1/nestedmod2/cmd/cmd6"),
		filepath.Join(dir, "mod2/cmd/cmd3"),
		filepath.Join(dir, "mod2/cmd/cmd4"),
		filepath.Join(dir, "nomod/cmd/cmd7"),
	}
	files := []string{
		filepath.Join(dir, "mod1/go.mod"),
		filepath.Join(dir, "mod1/nestedmod1/go.mod"),
		filepath.Join(dir, "mod1/nestedmod2/go.mod"),
		filepath.Join(dir, "mod2/go.mod"),
	}

	for _, path := range paths {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Errorf("Failed to create dir: %v", err)
		}
	}
	for _, file := range files {
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Errorf("Failed to create file: %v", err)
		}
	}

	mods, noModulePkgs := modules(paths)

	want := map[string][]string{
		filepath.Join(dir, "mod1"): {
			filepath.Join(dir, "mod1/cmd/cmd1"),
			filepath.Join(dir, "mod1/cmd/cmd2"),
		},
		filepath.Join(dir, "mod1/nestedmod1"): {
			filepath.Join(dir, "mod1/nestedmod1/cmd/cmd5"),
		},
		filepath.Join(dir, "mod1/nestedmod2"): {
			filepath.Join(dir, "mod1/nestedmod2/cmd/cmd6"),
		},
		filepath.Join(dir, "mod2"): {
			filepath.Join(dir, "mod2/cmd/cmd3"),
			filepath.Join(dir, "mod2/cmd/cmd4"),
		},
	}
	if !reflect.DeepEqual(mods, want) {
		t.Errorf("modules() = %v, want %v", mods, want)
	}
	wantNoModule := []string{
		filepath.Join(dir, "nomod/cmd/cmd7"),
	}
	if !reflect.DeepEqual(noModulePkgs, wantNoModule) {
		t.Errorf("modules() no module pkgs = %v, want %v", noModulePkgs, wantNoModule)
	}
}
