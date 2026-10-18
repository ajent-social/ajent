// SPDX-License-Identifier: Apache-2.0

package update

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// resetSeams captures the current seam values and restores them when the
// calling (sub)test finishes, so cases can freely override them.
func resetSeams(t *testing.T) {
	t.Helper()

	origGoos, origDpkg, origRunCmd, origGetenv, origEval := goos, dpkgInfoDir, runCmd, getenv, evalSymlinks
	t.Cleanup(func() {
		goos = origGoos
		dpkgInfoDir = origDpkg
		runCmd = origRunCmd
		getenv = origGetenv
		evalSymlinks = origEval
	})
}

// stubGetenv replaces the getenv seam with one backed by vals, defaulting
// unlisted keys to "" so a case never sees the real host environment.
func stubGetenv(t *testing.T, vals map[string]string) {
	t.Helper()

	getenv = func(key string) string {
		return vals[key]
	}
}

// writeInstallJSON writes dir/install.json with the given method and path,
// mirroring the format the install scripts write (docs/tasks/T5.9.md 2.9).
func writeInstallJSON(t *testing.T, dir, method, path string) {
	t.Helper()

	rec := struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	}{method, path}

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.json"), data, 0o600); err != nil {
		t.Fatalf("os.WriteFile: %v", err)
	}
}

type detectCase struct {
	name  string
	setup func(t *testing.T) (executable, configDir string)
	want  Method
}

func TestDetect(t *testing.T) {
	cases := []detectCase{
		{
			name: "script-matching-path",
			setup: func(t *testing.T) (string, string) {
				resetSeams(t)
				stubGetenv(t, nil)
				dir := t.TempDir()
				exe := filepath.Join(dir, "ajent")
				cfg := t.TempDir()
				writeInstallJSON(t, cfg, "script", exe)
				return exe, cfg
			},
			want: MethodScript,
		},
		{
			name: "script-json-other-path",
			setup: func(t *testing.T) (string, string) {
				resetSeams(t)
				stubGetenv(t, nil)
				dir := t.TempDir()
				exe := filepath.Join(dir, "ajent")
				cfg := t.TempDir()
				writeInstallJSON(t, cfg, "script", filepath.Join(dir, "other", "ajent"))
				return exe, cfg
			},
			want: MethodUnknown,
		},
		{
			name: "script-json-symlink",
			setup: func(t *testing.T) (string, string) {
				if runtime.GOOS == "windows" {
					t.Skip("creating symlinks requires elevated privileges on windows")
				}
				resetSeams(t)
				stubGetenv(t, nil)
				dir := t.TempDir()
				exe := filepath.Join(dir, "ajent")
				if err := os.WriteFile(exe, []byte("bin"), 0o600); err != nil {
					t.Fatalf("os.WriteFile: %v", err)
				}
				link := filepath.Join(dir, "ajent-link")
				if err := os.Symlink(exe, link); err != nil {
					t.Skipf("os.Symlink: %v", err)
				}
				cfg := t.TempDir()
				writeInstallJSON(t, cfg, "script", link)
				return exe, cfg
			},
			want: MethodScript,
		},
		{
			// Genuine-red case (contract acceptance criteria): with rules 1
			// and 2 swapped, a script install living under a Homebrew-shaped
			// path is misclassified as homebrew. See detect.go's rule order.
			name: "script-in-brew-like-path",
			setup: func(t *testing.T) (string, string) {
				resetSeams(t)
				stubGetenv(t, nil)
				exe := filepath.FromSlash("/opt/homebrew/Cellar/ajent/x/ajent")
				cfg := t.TempDir()
				writeInstallJSON(t, cfg, "script", exe)
				return exe, cfg
			},
			want: MethodScript,
		},
		{
			name: "brew-cellar-arm",
			setup: func(t *testing.T) (string, string) {
				resetSeams(t)
				stubGetenv(t, nil)
				return filepath.FromSlash("/opt/homebrew/Cellar/ajent/0.7.0/bin/ajent"), t.TempDir()
			},
			want: MethodHomebrew,
		},
		{
			name: "brew-caskroom",
			setup: func(t *testing.T) (string, string) {
				resetSeams(t)
				stubGetenv(t, nil)
				return filepath.FromSlash("/opt/homebrew/Caskroom/ajent/0.7.0/ajent"), t.TempDir()
			},
			want: MethodHomebrew,
		},
		{
			name: "brew-intel-symlink",
			setup: func(t *testing.T) (string, string) {
				resetSeams(t)
				stubGetenv(t, nil)
				exe := filepath.FromSlash("/usr/local/bin/ajent")
				target := filepath.FromSlash("/usr/local/Caskroom/ajent/0.7.0/ajent")
				evalSymlinks = func(p string) (string, error) {
					if p == exe {
						return target, nil
					}
					return "", os.ErrNotExist
				}
				return exe, t.TempDir()
			},
			want: MethodHomebrew,
		},
		{
			name: "dpkg-owned",
			setup: func(t *testing.T) (string, string) {
				resetSeams(t)
				stubGetenv(t, nil)
				goos = "linux"
				dpkgInfoDir = t.TempDir()
				if err := os.WriteFile(filepath.Join(dpkgInfoDir, "ajent.list"), []byte("/usr/bin/ajent\n"), 0o600); err != nil {
					t.Fatalf("os.WriteFile: %v", err)
				}
				return filepath.FromSlash("/usr/bin/ajent"), t.TempDir()
			},
			want: MethodDpkg,
		},
		{
			name: "dpkg-list-missing-rpm-owned",
			setup: func(t *testing.T) (string, string) {
				resetSeams(t)
				stubGetenv(t, nil)
				goos = "linux"
				dpkgInfoDir = t.TempDir() // no ajent.list
				runCmd = func(_ string, _ ...string) ([]byte, error) {
					return []byte("ajent"), nil
				}
				return filepath.FromSlash("/usr/bin/ajent"), t.TempDir()
			},
			want: MethodRPM,
		},
		{
			name: "usr-bin-unowned",
			setup: func(t *testing.T) (string, string) {
				resetSeams(t)
				stubGetenv(t, nil)
				goos = "linux"
				dpkgInfoDir = t.TempDir()
				runCmd = func(_ string, _ ...string) ([]byte, error) {
					return nil, errors.New("no such package")
				}
				return filepath.FromSlash("/usr/bin/ajent"), t.TempDir()
			},
			want: MethodUnknown,
		},
		{
			name: "scoop-app",
			setup: func(t *testing.T) (string, string) {
				resetSeams(t)
				stubGetenv(t, nil)
				goos = "windows"
				return `C:\Users\u\scoop\apps\ajent\current\ajent.exe`, t.TempDir()
			},
			want: MethodScoop,
		},
		{
			name: "winget-package",
			setup: func(t *testing.T) (string, string) {
				resetSeams(t)
				stubGetenv(t, nil)
				goos = "windows"
				return `C:\Users\u\AppData\Local\Microsoft\WinGet\Packages\AjentSocial.Ajent_Microsoft.Winget.Source_8wekyb3d8bbwe\ajent.exe`, t.TempDir()
			},
			want: MethodWinget,
		},
		{
			name: "winget-link",
			setup: func(t *testing.T) (string, string) {
				resetSeams(t)
				stubGetenv(t, nil)
				goos = "windows"
				return `C:\Users\u\AppData\Local\Microsoft\WinGet\Links\ajent.exe`, t.TempDir()
			},
			want: MethodWinget,
		},
		{
			name: "go-install-gobin",
			setup: func(t *testing.T) (string, string) {
				resetSeams(t)
				gobin := filepath.FromSlash("/home/dev/go-bin")
				stubGetenv(t, map[string]string{"GOBIN": gobin})
				return filepath.Join(gobin, "ajent"), t.TempDir()
			},
			want: MethodGoInstall,
		},
		{
			name: "go-install-gopath",
			setup: func(t *testing.T) (string, string) {
				resetSeams(t)
				gopath := filepath.FromSlash("/home/dev/go")
				stubGetenv(t, map[string]string{"GOPATH": gopath})
				return filepath.Join(gopath, "bin", "ajent"), t.TempDir()
			},
			want: MethodGoInstall,
		},
		{
			name: "unknown-path",
			setup: func(t *testing.T) (string, string) {
				resetSeams(t)
				stubGetenv(t, nil)
				return filepath.FromSlash("/tmp/x/ajent"), t.TempDir()
			},
			want: MethodUnknown,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			executable, configDir := tc.setup(t)
			if got := Detect(executable, configDir); got != tc.want {
				t.Errorf("Detect(%q, %q) = %q, want %q", executable, configDir, got, tc.want)
			}
		})
	}
}

func TestUpgradeCommand(t *testing.T) {
	cases := []struct {
		method Method
		want   string
	}{
		{MethodHomebrew, "brew upgrade ajent"},
		{MethodDpkg, "sudo apt update && sudo apt install --only-upgrade ajent"},
		{MethodRPM, "sudo dnf upgrade ajent"},
		{MethodScoop, "scoop update ajent"},
		{MethodWinget, "winget upgrade --id AjentSocial.Ajent --exact"},
		{MethodGoInstall, "go install github.com/ajent-social/ajent-client/cmd/ajent@latest"},
		{MethodScript, ""},
		{MethodUnknown, ""},
	}

	for _, tc := range cases {
		t.Run(string(tc.method), func(t *testing.T) {
			if got := UpgradeCommand(tc.method); got != tc.want {
				t.Errorf("UpgradeCommand(%q) = %q, want %q", tc.method, got, tc.want)
			}
		})
	}
}

func TestManaged(t *testing.T) {
	cases := []struct {
		method Method
		want   bool
	}{
		{MethodHomebrew, true},
		{MethodDpkg, true},
		{MethodRPM, true},
		{MethodScoop, true},
		{MethodWinget, true},
		{MethodGoInstall, true},
		{MethodScript, false},
		{MethodUnknown, false},
	}

	for _, tc := range cases {
		t.Run(string(tc.method), func(t *testing.T) {
			if got := tc.method.Managed(); got != tc.want {
				t.Errorf("%q.Managed() = %v, want %v", tc.method, got, tc.want)
			}
		})
	}
}

func TestLabel(t *testing.T) {
	cases := []struct {
		method Method
		want   string
	}{
		{MethodHomebrew, "Homebrew"},
		{MethodDpkg, "APT"},
		{MethodRPM, "DNF"},
		{MethodScoop, "Scoop"},
		{MethodWinget, "winget"},
		{MethodGoInstall, "go install"},
		{MethodScript, "the install script"},
		{MethodUnknown, "an unknown method"},
	}

	for _, tc := range cases {
		t.Run(string(tc.method), func(t *testing.T) {
			if got := tc.method.Label(); got != tc.want {
				t.Errorf("%q.Label() = %q, want %q", tc.method, got, tc.want)
			}
		})
	}
}
