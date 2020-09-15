// Copyright 2018 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package findpkg

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestModules(t *testing.T) {
	dir, err := ioutil.TempDir("", "test-modules-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	os.MkdirAll(filepath.Join(dir, "mod1/cmd/cmd1"), 0755)
	os.MkdirAll(filepath.Join(dir, "mod1/cmd/cmd2"), 0755)
	os.MkdirAll(filepath.Join(dir, "mod1/nestedmod1/cmd/cmd5"), 0755)
	os.MkdirAll(filepath.Join(dir, "mod1/nestedmod2/cmd/cmd6"), 0755)
	os.MkdirAll(filepath.Join(dir, "mod2/cmd/cmd3"), 0755)
	os.MkdirAll(filepath.Join(dir, "mod2/cmd/cmd4"), 0755)
	os.MkdirAll(filepath.Join(dir, "nomod/cmd/cmd7"), 0755)
	ioutil.WriteFile(filepath.Join(dir, "mod1/go.mod"), nil, 0644)
	ioutil.WriteFile(filepath.Join(dir, "mod1/nestedmod1/go.mod"), nil, 0644)
	ioutil.WriteFile(filepath.Join(dir, "mod1/nestedmod2/go.mod"), nil, 0644)
	ioutil.WriteFile(filepath.Join(dir, "mod2/go.mod"), nil, 0644)

	paths := []string{
		filepath.Join(dir, "mod1/cmd/cmd1"),
		filepath.Join(dir, "mod1/cmd/cmd2"),
		filepath.Join(dir, "mod1/nestedmod1/cmd/cmd5"),
		filepath.Join(dir, "mod1/nestedmod2/cmd/cmd6"),
		filepath.Join(dir, "mod2/cmd/cmd3"),
		filepath.Join(dir, "mod2/cmd/cmd4"),
		filepath.Join(dir, "nomod/cmd/cmd7"),
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
