package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/u-root/gobusybox/src/pkg/golang"
)

var makebb = flag.String("makebb", "", "makebb binary path")

func skipIfUnsupported(t *testing.T, goVersion string, unsupportedGoVersions []string) {
	for _, unsupportedGoVersion := range unsupportedGoVersions {
		// Contains so that go1.13 also matches e.g. go1.13.1
		if strings.Contains(goVersion, unsupportedGoVersion) {
			t.Skipf("Version %s is unsupported for this test (unsupported versions: %v)", goVersion, unsupportedGoVersion)
		}
	}
}

func TestMakeBB(t *testing.T) {
	if *makebb == "" {
		t.Fatalf("Path to makebb is not set")
	}

	goVersion, err := golang.Default().Version()
	if err != nil {
		t.Fatalf("Could not determine Go version: %v", err)
	}

	for _, tt := range []struct {
		testname string
		// file paths to commands to compile
		cmds []string
		// extra args to makebb
		extraArgs []string
		// command name -> expected output
		want map[string]string
		// Go versions for which this test should be skipped.
		unsupportedGoVersions []string
	}{
		{
			testname:              "goembed",
			cmds:                  []string{"./goembed"},
			want:                  map[string]string{"goembed": "hello\n"},
			unsupportedGoVersions: []string{"go1.15"},
		},
		{
			testname: "12-fancy-cmd",
			cmds:     []string{"./12-fancy-cmd"},
			want:     map[string]string{"12-fancy-cmd": "12-fancy-cmd\n"},
		},
		{
			testname: "globalvar",
			cmds:     []string{"./globalvar"},
			want:     map[string]string{"globalvar": "foo bar\n"},
		},
		{
			testname:  "injectldvar",
			cmds:      []string{"./injectldvar"},
			extraArgs: []string{"-go-extra-args=-ldflags", "-go-extra-args=-X 'github.com/u-root/gobusybox/test/injectldvar.Something=Hello World'"},
			want:      map[string]string{"injectldvar": "Hello World\n"},
		},
		{
			testname: "implicitimport",
			cmds:     []string{"./implicitimport/cmd/loghello"},
			want:     map[string]string{"loghello": "Log Hello\n"},
		},
		{
			testname: "nested-modules",
			cmds:     []string{"./nested/cmd/dmesg", "./nested/cmd/strace", "./nested/nestedmod/cmd/p9ufs"},
		},
		{
			testname: "cross-module-deps",
			cmds:     []string{"./normaldeps/mod1/cmd/helloworld", "./normaldeps/mod1/cmd/getppid"},
			want: map[string]string{
				"helloworld": "test/normaldeps/mod2/hello: test/normaldeps/mod2/v2/hello\n",
				"getppid":    fmt.Sprintf("%d\n", os.Getpid()),
			},
		},
		{
			testname: "import-name-conflict",
			cmds:     []string{"./nameconflict/cmd/nameconflict"},
		},
		{
			testname: "diamond-module-dependency",
			cmds:     []string{"./diamonddep/mod1/cmd/hellowithdep", "./diamonddep/mod1/cmd/helloworld"},
			want: map[string]string{
				"hellowithdep": "test/diamonddep/mod1/hello: test/diamonddep/mod1/hello\n" +
					"test/diamonddep/mod2/hello: test/diamonddep/mod2/hello\n" +
					"test/diamonddep/mod2/exthello: test/diamonddep/mod2/exthello: test/diamonddep/mod1/hello and test/diamonddep/mod3/hello\n",
				"helloworld": "hello world\n",
			},
		},
	} {
		t.Run(tt.testname, func(t *testing.T) {
			skipIfUnsupported(t, goVersion, tt.unsupportedGoVersions)

			dir, err := ioutil.TempDir("", tt.testname)
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)

			for _, go111module := range []string{"on", "auto"} {
				goEnv := "GO111MODULE=" + go111module
				t.Run(goEnv, func(t *testing.T) {
					binary := filepath.Join(dir, fmt.Sprintf("bb-%s", go111module))

					// Build the bb.
					t.Logf("Run: %s %s -o %s %v %s", goEnv, *makebb, binary, strings.Join(tt.extraArgs, " "), strings.Join(tt.cmds, " "))
					args := append([]string{"-o", binary}, tt.extraArgs...)
					cmd := exec.Command(*makebb, append(args, tt.cmds...)...)
					cmd.Env = append(os.Environ(), goEnv)
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Logf("makebb: %s", string(out))
						t.Fatalf("cmd: %v", err)
					}

					// There are some builds for which we
					// don't check the output since it's
					// unpredictable. We at least want to
					// check the binary exists.
					if _, err := os.Stat(binary); err != nil {
						t.Fatalf("Busybox binary does not exist: %v", err)
					}

					// Make sure that the bb contains all
					// the commands it's supposed to by
					// invoking them and checking their
					// output.
					for cmdName, want := range tt.want {
						t.Logf("Run: %s %s", binary, cmdName)
						out, err = exec.Command(binary, cmdName).CombinedOutput()
						if err != nil {
							t.Fatalf("cmd: %v", err)
						}
						if got := string(out); got != want {
							t.Errorf("Output of %s = %v, want %v", cmdName, got, want)
						}
					}
				})
			}

			// Make sure that bb is reproducible.
			binaryOn, err := ioutil.ReadFile(filepath.Join(dir, "bb-on"))
			if err != nil {
				t.Errorf("bb binary for GO111MODULE=on does not exist: %v", err)
			}
			binaryAuto, err := ioutil.ReadFile(filepath.Join(dir, "bb-auto"))
			if err != nil {
				t.Errorf("bb binary for GO111MODULE=auto does not exist: %v", err)
			}
			if !bytes.Equal(binaryOn, binaryAuto) {
				t.Errorf("bb not reproducible")
			}
		})
	}
}
