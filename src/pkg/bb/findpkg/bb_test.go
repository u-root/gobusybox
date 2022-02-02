// Copyright 2018 the u-root Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package findpkg

import (
	"errors"
	"go/build"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"github.com/u-root/gobusybox/src/pkg/golang"
	"github.com/u-root/uio/ulog"
	"golang.org/x/tools/go/packages"
)

const (
	modTemplate = `module {{ .ModPath }}

go 1.16
`

	sourceTemplate = `package main

func main() {
	return
}
`
)

type module struct {
	ModPath string
}

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

func TestLoadFSPackages(t *testing.T) {
	for _, tt := range []struct {
		name     string
		modules  string
		paths    []string
		modFiles []string
		goFiles  []string
		wantErr  error
	}{
		{
			name:    "modules",
			modules: "on",
			paths: []string{
				"mod1/cmd/cmd1",
				"mod1/cmd/cmd2",
				"mod1/nestedmod1/cmd/cmd5",
				"mod1/nestedmod2/cmd/cmd6",
				"mod2/cmd/cmd3",
				"mod2/cmd/cmd4",
			},
			modFiles: []string{
				"mod1/go.mod",
				"mod1/nestedmod1/go.mod",
				"mod1/nestedmod2/go.mod",
				"mod2/go.mod",
			},
			goFiles: []string{
				"mod1/cmd/cmd1/main.go",
				"mod1/cmd/cmd2/main.go",
				"mod1/nestedmod1/cmd/cmd5/main.go",
				"mod1/nestedmod2/cmd/cmd6/main.go",
				"mod2/cmd/cmd3/main.go",
				"mod2/cmd/cmd4/main.go",
			},
			wantErr: nil,
		},
		{
			name:    "no modules",
			modules: "off",
			paths: []string{
				"nomod1/cmd/cmd1",
				"nomod1/cmd/cmd2",
				"nomod2/cmd/cmd3",
				"nomod2/cmd/cmd4",
			},
			modFiles: []string{},
			goFiles: []string{
				"nomod1/cmd/cmd1/main.go",
				"nomod1/cmd/cmd2/main.go",
				"nomod2/cmd/cmd3/main.go",
				"nomod2/cmd/cmd4/main.go",
			},
			wantErr: nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Set temporary working dir and environment
			dir := t.TempDir()
			env := golang.Environ{
				Context:     build.Default,
				GO111MODULE: tt.modules,
			}

			// Create temoprary folder structure
			paths := make([]string, len(tt.paths))
			for i, path := range tt.paths {
				paths[i] = filepath.Join(dir, path)
				if err := os.MkdirAll(paths[i], 0o755); err != nil {
					t.Errorf("Failed to create dir: %v", err)
				}
			}

			// Set go.mod file template to replace the module path later
			tmpl, err := template.New("mod").Parse(modTemplate)
			if err != nil {
				t.Errorf("Failed to parse template: %v", err)
			}

			// Create corresponding go.mod files and fill in the template
			modFiles := make([]string, len(tt.modFiles))
			for i, file := range tt.modFiles {
				modFiles[i] = filepath.Join(dir, file)
				f, err := os.Create(modFiles[i])
				if err != nil {
					t.Errorf("Failed to create file: %v", err)
				}

				if err := tmpl.Execute(f, module{
					ModPath: strings.TrimPrefix(filepath.Dir(modFiles[i]), "/tmp/"),
				}); err != nil {
					t.Errorf("Failed writing to file: %v", err)
				}

				if err := f.Close(); err != nil {
					t.Errorf("Failed to close file: %v", err)
				}
			}

			// Create main.go files
			goFiles := make([]string, len(tt.goFiles))
			for i, file := range tt.goFiles {
				goFiles[i] = filepath.Join(dir, file)
				if err := os.WriteFile(goFiles[i], []byte(sourceTemplate), 0o644); err != nil {
					t.Errorf("Failed to create file: %v", err)
				}
			}

			// Call into function to test
			// TODO(MDr164): We should probably use errors.Is() but then we need gloabl error objects
			if _, err := loadFSPackages(ulog.Null, env, paths); err != nil && tt.wantErr != nil {
				if !strings.Contains(err.Error(), tt.wantErr.Error()) {
					t.Errorf("loadFSPackages() err = %v, want: %v", err, tt.wantErr)
				}
			} else if (err != nil && tt.wantErr == nil) || (err == nil && tt.wantErr != nil) {
				t.Errorf("loadFSPackages() err = %v, want: %v", err, tt.wantErr)
			}
		})
	}
}

func TestAddPkg(t *testing.T) {
	for _, tt := range []struct {
		name string
		pkg  packages.Package
	}{
		{
			name: "empty package",
			pkg:  packages.Package{},
		},
		{
			name: "not main",
			pkg: packages.Package{
				Name:    "cmd",
				GoFiles: []string{"cmd.go"},
			},
		},
		{
			name: "error",
			pkg: packages.Package{
				Errors: []packages.Error{{Msg: "error"}},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			addPkg(ulog.Null, nil, &tt.pkg)
		})
	}
}

func TestNewPackages(t *testing.T) {
	for _, tt := range []struct {
		name     string
		modules  string
		pkgNames []string
		wantErr  error
	}{
		{
			name:    "empty path",
			modules: "on",
			pkgNames: []string{
				"",
			},
			wantErr: nil,
		},
		{
			name:    "filepath",
			modules: "on",
			pkgNames: []string{
				"./",
			},
			wantErr: nil,
		},
		{
			name:    "invalid filesystem path",
			modules: "on",
			pkgNames: []string{
				"/bogus",
			},
			wantErr: errors.New("could not load packages from file system"),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := golang.Environ{
				Context:     build.Default,
				GO111MODULE: tt.modules,
			}

			if _, err := NewPackages(ulog.Null, env, tt.pkgNames...); err != nil && tt.wantErr != nil {
				if !strings.Contains(err.Error(), tt.wantErr.Error()) {
					t.Errorf("loadFSPackages() err = %v, want: %v", err, tt.wantErr)
				}
			} else if (err != nil && tt.wantErr == nil) || (err == nil && tt.wantErr != nil) {
				t.Errorf("loadFSPackages() err = %v, want: %v", err, tt.wantErr)
			}
		})
	}
}
