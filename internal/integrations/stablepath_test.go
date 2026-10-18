// SPDX-License-Identifier: Apache-2.0

package integrations

import "testing"

func getenvWith(vals map[string]string) func(string) string {
	return func(k string) string { return vals[k] }
}

func existsSet(paths ...string) func(string) bool {
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	return func(p string) bool { return set[p] }
}

func TestStableBinary(t *testing.T) {
	cases := []struct {
		name       string
		executable string
		goos       string
		getenv     func(string) string
		exists     func(string) bool
		want       string
	}{
		{
			name:       "homebrew formula darwin",
			executable: "/opt/homebrew/Cellar/ajent/1.2.3/bin/ajent",
			goos:       "darwin",
			getenv:     getenvWith(nil),
			exists:     existsSet("/opt/homebrew/bin/ajent"),
			want:       "/opt/homebrew/bin/ajent",
		},
		{
			name:       "homebrew formula linux (linuxbrew)",
			executable: "/home/linuxbrew/.linuxbrew/Cellar/ajent/1.2.3/bin/ajent",
			goos:       "linux",
			getenv:     getenvWith(nil),
			exists:     existsSet("/home/linuxbrew/.linuxbrew/bin/ajent"),
			want:       "/home/linuxbrew/.linuxbrew/bin/ajent",
		},
		{
			name:       "homebrew formula stable link missing falls back to executable",
			executable: "/opt/homebrew/Cellar/ajent/1.2.3/bin/ajent",
			goos:       "darwin",
			getenv:     getenvWith(nil),
			exists:     existsSet(),
			want:       "/opt/homebrew/Cellar/ajent/1.2.3/bin/ajent",
		},
		{
			name:       "homebrew cask darwin",
			executable: "/opt/homebrew/Caskroom/ajent/1.2.3/ajent",
			goos:       "darwin",
			getenv:     getenvWith(nil),
			exists:     existsSet("/opt/homebrew/bin/ajent"),
			want:       "/opt/homebrew/bin/ajent",
		},
		{
			name:       "homebrew cask stable link missing falls back to executable",
			executable: "/opt/homebrew/Caskroom/ajent/1.2.3/ajent",
			goos:       "darwin",
			getenv:     getenvWith(nil),
			exists:     existsSet(),
			want:       "/opt/homebrew/Caskroom/ajent/1.2.3/ajent",
		},
		{
			name:       "winget windows",
			executable: `C:\HOME\AppData\Local\Microsoft\WinGet\Packages\ajent-social.ajent_Microsoft.Winget.Source_abc\ajent.exe`,
			goos:       "windows",
			getenv:     getenvWith(map[string]string{"LOCALAPPDATA": `C:\HOME\AppData\Local`}),
			exists:     existsSet(`C:\HOME\AppData\Local\Microsoft\WinGet\Links\ajent.exe`),
			want:       `C:\HOME\AppData\Local\Microsoft\WinGet\Links\ajent.exe`,
		},
		{
			name:       "winget match is case-insensitive",
			executable: `C:\HOME\AppData\Local\microsoft\winget\packages\ajent-social.ajent_abc\ajent.exe`,
			goos:       "windows",
			getenv:     getenvWith(map[string]string{"LOCALAPPDATA": `C:\HOME\AppData\Local`}),
			exists:     existsSet(`C:\HOME\AppData\Local\Microsoft\WinGet\Links\ajent.exe`),
			want:       `C:\HOME\AppData\Local\Microsoft\WinGet\Links\ajent.exe`,
		},
		{
			name:       "winget link missing falls back to executable",
			executable: `C:\HOME\AppData\Local\Microsoft\WinGet\Packages\ajent-social.ajent_abc\ajent.exe`,
			goos:       "windows",
			getenv:     getenvWith(map[string]string{"LOCALAPPDATA": `C:\HOME\AppData\Local`}),
			exists:     existsSet(),
			want:       `C:\HOME\AppData\Local\Microsoft\WinGet\Packages\ajent-social.ajent_abc\ajent.exe`,
		},
		{
			name:       "scoop windows",
			executable: `C:\HOME\scoop\apps\ajent\1.4.0\ajent.exe`,
			goos:       "windows",
			getenv:     getenvWith(nil),
			exists:     existsSet(`C:\HOME\scoop\apps\ajent\current\ajent.exe`),
			want:       `C:\HOME\scoop\apps\ajent\current\ajent.exe`,
		},
		{
			name:       "scoop already current is left unchanged",
			executable: `C:\HOME\scoop\apps\ajent\current\ajent.exe`,
			goos:       "windows",
			getenv:     getenvWith(nil),
			exists:     existsSet(`C:\HOME\scoop\apps\ajent\current\ajent.exe`),
			want:       `C:\HOME\scoop\apps\ajent\current\ajent.exe`,
		},
		{
			name:       "scoop current shim missing falls back to executable",
			executable: `C:\HOME\scoop\apps\ajent\1.4.0\ajent.exe`,
			goos:       "windows",
			getenv:     getenvWith(nil),
			exists:     existsSet(),
			want:       `C:\HOME\scoop\apps\ajent\1.4.0\ajent.exe`,
		},
		{
			name:       "no rule matches on linux",
			executable: "/usr/local/bin/ajent",
			goos:       "linux",
			getenv:     getenvWith(nil),
			exists:     existsSet("/usr/local/bin/ajent"),
			want:       "/usr/local/bin/ajent",
		},
		{
			name:       "go install path unchanged",
			executable: "/HOME/go/bin/ajent",
			goos:       "darwin",
			getenv:     getenvWith(nil),
			exists:     existsSet(),
			want:       "/HOME/go/bin/ajent",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := StableBinary(tc.executable, tc.goos, tc.getenv, tc.exists)
			if got != tc.want {
				t.Errorf("StableBinary(%q, %q) = %q, want %q", tc.executable, tc.goos, got, tc.want)
			}
		})
	}
}

func TestParamsFromEnv(t *testing.T) {
	prev := BinaryOverride
	BinaryOverride = "/HOME/go/bin/ajent"
	defer func() { BinaryOverride = prev }()

	home := "/HOME"
	configDir := "/HOME/.aj"
	getenv := getenvWith(map[string]string{"AJENT_CREDENTIAL_FILE": "/HOME/.aj/credential"})

	p, err := ParamsFromEnv(home, configDir, getenv)
	if err != nil {
		t.Fatalf("ParamsFromEnv() error = %v", err)
	}
	if p.Binary != "/HOME/go/bin/ajent" {
		t.Errorf("Binary = %q, want BinaryOverride value", p.Binary)
	}
	if p.Home != home || p.ConfigDir != configDir {
		t.Errorf("Home/ConfigDir = %q/%q, want %q/%q", p.Home, p.ConfigDir, home, configDir)
	}
	if p.CredentialFile != "/HOME/.aj/credential" {
		t.Errorf("CredentialFile = %q, want /HOME/.aj/credential", p.CredentialFile)
	}
	if p.Getenv == nil {
		t.Error("Getenv is nil, want the passed function")
	}
}
