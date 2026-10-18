// SPDX-License-Identifier: Apache-2.0

package credentials

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ajent-social/ajent/internal/config"
)

// TestLoadPythonFixtures loads the Python reference client's on-disk
// layout (testdata/contract/files/home/.aj, produced by T2.1) and checks
// that LoadRoot and LoadProfile read it: the root token matches, and both
// a completed and a pending profile fixture are found and reported
// correctly, without hardcoding either fixture's profile name.
func TestLoadPythonFixtures(t *testing.T) {
	src := filepath.Join("..", "..", "testdata", "contract", "files", "home", ".aj")
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			t.Skip("testdata/contract/files not present yet (lands with T2.1)")
		}
		t.Fatalf("Stat(%s): %v", src, err)
	}

	dir := t.TempDir()
	copyDir(t, src, dir)

	root, err := LoadRoot(dir)
	if err != nil {
		t.Fatalf("LoadRoot() error = %v", err)
	}
	const wantToken = "aj_0123456789abcdef_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if root.Token != wantToken {
		t.Fatalf("LoadRoot().Token = %q, want %q", root.Token, wantToken)
	}

	profileDir := ProfileDir(dir, root)
	entries, err := os.ReadDir(profileDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", profileDir, err)
	}

	var sawCompleted, sawPending bool
	for _, e := range entries {
		name, isJSON := strings.CutSuffix(e.Name(), ".json")
		if !isJSON {
			continue
		}
		c, ok, err := LoadProfile(dir, root, name)
		if err != nil {
			t.Fatalf("LoadProfile(%s) error = %v", name, err)
		}
		if !ok {
			t.Fatalf("LoadProfile(%s) ok = false, want true", name)
		}
		if c.Pending {
			sawPending = true
		} else {
			sawCompleted = true
		}
	}
	if !sawCompleted {
		t.Error("fixture directory has no completed (non-pending) profile")
	}
	if !sawPending {
		t.Error("fixture directory has no pending profile")
	}
}

// TestRoundTripKeepsUnknownFields checks that a field LoadRoot does not
// know about survives a LoadRoot/SaveRoot round trip.
func TestRoundTripKeepsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	initial := `{"server":"https://ajent.social","token":"aj_test","future_field":{"x":1}}`
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(RootPath(dir), []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	c, err := LoadRoot(dir)
	if err != nil {
		t.Fatalf("LoadRoot() error = %v", err)
	}
	if _, ok := c.Extra["future_field"]; !ok {
		t.Fatalf("LoadRoot().Extra missing future_field: %+v", c.Extra)
	}

	if err := SaveRoot(dir, c); err != nil {
		t.Fatalf("SaveRoot() error = %v", err)
	}

	data, err := os.ReadFile(RootPath(dir))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	future, ok := got["future_field"].(map[string]any)
	if !ok || future["x"] != float64(1) {
		t.Fatalf("future_field = %#v, want {x:1}", got["future_field"])
	}
}

// TestTokenEnvOverride checks that AJENT_TOKEN, a Go addition absent from
// the Python client, overrides the stored root credential entirely and
// never touches the filesystem.
func TestTokenEnvOverride(t *testing.T) {
	t.Run("default origin", func(t *testing.T) {
		t.Setenv("AJENT_TOKEN", "x")

		// dir contains no credential.json; LoadRoot must succeed anyway.
		dir := t.TempDir()

		c, err := LoadRoot(dir)
		if err != nil {
			t.Fatalf("LoadRoot() error = %v", err)
		}
		if c.Token != "x" {
			t.Fatalf("Token = %q, want %q", c.Token, "x")
		}
		if c.Server != defaultOrigin {
			t.Fatalf("Server = %q, want %q", c.Server, defaultOrigin)
		}
	})

	t.Run("AJENT_URL overrides origin", func(t *testing.T) {
		t.Setenv("AJENT_TOKEN", "x")
		t.Setenv("AJENT_URL", "https://example.com")

		c, err := LoadRoot(t.TempDir())
		if err != nil {
			t.Fatalf("LoadRoot() error = %v", err)
		}
		if c.Server != "https://example.com" {
			t.Fatalf("Server = %q, want %q", c.Server, "https://example.com")
		}
	})
}

// TestCredentialFileEnv checks that AJENT_CREDENTIAL_FILE redirects both
// RootPath and the LoadRoot/SaveRoot pair away from dir/credential.json.
func TestCredentialFileEnv(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(t.TempDir(), "elsewhere.json")
	t.Setenv("AJENT_CREDENTIAL_FILE", other)

	if got := RootPath(dir); got != other {
		t.Fatalf("RootPath(%q) = %q, want %q", dir, got, other)
	}

	c := Credential{Server: "https://ajent.social", Token: "aj_test"}
	if err := SaveRoot(dir, c); err != nil {
		t.Fatalf("SaveRoot() error = %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("Stat(%s): %v", other, err)
	}

	got, err := LoadRoot(dir)
	if err != nil {
		t.Fatalf("LoadRoot() error = %v", err)
	}
	if got.Token != c.Token {
		t.Fatalf("LoadRoot().Token = %q, want %q", got.Token, c.Token)
	}
}

// TestRootPathExpandsTilde checks the leading "~" expansion RootPath
// documents.
func TestRootPathExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory available")
	}
	t.Setenv("AJENT_CREDENTIAL_FILE", filepath.Join("~", "cred.json"))

	want := filepath.Join(home, "cred.json")
	if got := RootPath(t.TempDir()); got != want {
		t.Fatalf("RootPath() = %q, want %q", got, want)
	}
}

// TestLoadRootMissing checks that a missing root credential file reports
// ErrNoCredential rather than a bare filesystem error.
func TestLoadRootMissing(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadRoot(dir); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("LoadRoot() error = %v, want ErrNoCredential", err)
	}
}

// TestCreateRootExclusive checks that CreateRoot refuses to overwrite an
// existing root credential.
func TestCreateRootExclusive(t *testing.T) {
	dir := t.TempDir()
	c := Credential{Server: "https://ajent.social", Token: "aj_test"}

	if err := CreateRoot(dir, c); err != nil {
		t.Fatalf("first CreateRoot() error = %v", err)
	}
	if err := CreateRoot(dir, c); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second CreateRoot() error = %v, want fs.ErrExist", err)
	}
}

// TestLoadProfileInvalidName checks that LoadProfile validates the profile
// name before touching the filesystem.
func TestLoadProfileInvalidName(t *testing.T) {
	dir := t.TempDir()
	root := Credential{Token: "aj_test"}
	if _, _, err := LoadProfile(dir, root, "Not Valid"); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("LoadProfile() error = %v, want ErrInvalidProfile", err)
	}
}

// TestLoadProfileMissingReturnsNotOK checks that a missing profile file is
// not an error: callers use ok to detect "not yet enrolled".
func TestLoadProfileMissingReturnsNotOK(t *testing.T) {
	dir := t.TempDir()
	root := Credential{Token: "aj_test"}

	c, ok, err := LoadProfile(dir, root, "terminal")
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	if ok {
		t.Fatalf("LoadProfile() ok = true, want false")
	}
	if !reflect.DeepEqual(c, Credential{}) {
		t.Fatalf("LoadProfile() credential = %+v, want zero value", c)
	}
}

// TestSaveLoadProfileRoundTrip checks that SaveProfile and LoadProfile
// agree on ProfilePath's layout.
func TestSaveLoadProfileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	root := Credential{Token: "aj_test"}
	want := Credential{Server: "https://ajent.social", Token: "aj_profile", Pending: true}

	if err := SaveProfile(dir, root, "terminal", want); err != nil {
		t.Fatalf("SaveProfile() error = %v", err)
	}

	if _, err := os.Stat(ProfilePath(dir, root, "terminal")); err != nil {
		t.Fatalf("Stat(ProfilePath()): %v", err)
	}

	got, ok, err := LoadProfile(dir, root, "terminal")
	if err != nil {
		t.Fatalf("LoadProfile() error = %v", err)
	}
	if !ok {
		t.Fatalf("LoadProfile() ok = false, want true")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadProfile() = %+v, want %+v", got, want)
	}

	if err := SaveProfile(dir, root, "Not Valid", want); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("SaveProfile() error = %v, want ErrInvalidProfile", err)
	}
}

// TestLoadProfileSymlink checks that a symlinked profile file is refused
// rather than followed.
func TestLoadProfileSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are privileged on windows")
	}

	dir := t.TempDir()
	root := Credential{Token: "aj_test"}

	profileDir := ProfileDir(dir, root)
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	realFile := filepath.Join(profileDir, "real.json")
	if err := os.WriteFile(realFile, []byte(`{"server":"https://ajent.social","token":"t"}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	link := ProfilePath(dir, root, "terminal")
	if err := os.Symlink(realFile, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	if _, _, err := LoadProfile(dir, root, "terminal"); !errors.Is(err, config.ErrSymlink) {
		t.Fatalf("LoadProfile() error = %v, want config.ErrSymlink", err)
	}
}

// copyDir recursively copies src to dst, which must already exist. It is
// used to run tests against a private, writable copy of a read-only
// testdata fixture.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", src, err)
	}
	for _, e := range entries {
		from := filepath.Join(src, e.Name())
		to := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := os.MkdirAll(to, 0o700); err != nil {
				t.Fatalf("MkdirAll(%s): %v", to, err)
			}
			copyDir(t, from, to)
			continue
		}
		data, err := os.ReadFile(from) //nolint:gosec // src is a testdata fixture tree the test itself walks, not user input
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", from, err)
		}
		if err := os.WriteFile(to, data, 0o600); err != nil { //nolint:gosec // to is derived from the same controlled fixture tree
			t.Fatalf("WriteFile(%s): %v", to, err)
		}
	}
}
