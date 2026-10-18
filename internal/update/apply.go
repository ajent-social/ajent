// SPDX-License-Identifier: Apache-2.0

package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ajent-social/ajent-client/internal/version"
)

// ErrNoAsset is returned by Apply when r has no archive asset for the
// current platform, or is missing the shared checksums.txt or
// checksums.txt.sig assets.
var ErrNoAsset = errors.New("update: release has no asset for this platform")

// errSelfCheck is returned by Apply when the extracted binary's own
// "version --json" does not report the version Apply is trying to install.
var errSelfCheck = errors.New("update: new binary failed its self-check")

const (
	// applyArchiveTimeout bounds the archive download specifically (the
	// checksums and signature downloads use ctx as given, since they are
	// tiny and Apply's caller already bounds the overall operation).
	applyArchiveTimeout = 5 * time.Minute
	// applyMaxDownloadBytes caps every downloaded asset, so a misbehaving
	// or compromised server cannot make Apply buffer unbounded data.
	applyMaxDownloadBytes = 100 << 20 // 100 MiB
	// applyMaxEntryBytes caps the single extracted archive entry.
	applyMaxEntryBytes = 200 << 20 // 200 MiB
	// applySelfCheckTimeout bounds the extracted binary's smoke test.
	applySelfCheckTimeout = 10 * time.Second
)

// ArchiveName returns the release archive file name for version on
// goos/goarch, matching the layout GoReleaser produces and
// fakerelease.BuildAssets serves: "ajent_<version>_<goos>_<goarch>.tar.gz",
// or ".zip" on windows.
func ArchiveName(version, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return "ajent_" + version + "_" + goos + "_" + goarch + ext
}

// Apply downloads release r's archive for the running platform, verifies
// its Ed25519-signed checksums (T5.3), extracts and smoke-tests the new
// binary, and atomically replaces executable with it. It returns before
// touching the filesystem when verification fails, and rolls back (or
// simply never writes) on any later failure, so executable is left
// untouched whenever Apply returns a non-nil error.
func Apply(ctx context.Context, s *Source, r Release, executable string) error {
	name := ArchiveName(r.Version, runtime.GOOS, runtime.GOARCH)

	archiveURL, ok := r.Assets[name]
	if !ok {
		return ErrNoAsset
	}
	checksumsURL, ok := r.Assets["checksums.txt"]
	if !ok {
		return ErrNoAsset
	}
	sigURL, ok := r.Assets["checksums.txt.sig"]
	if !ok {
		return ErrNoAsset
	}

	client := s.HTTP
	if client == nil {
		client = http.DefaultClient
	}

	checksums, err := applyDownload(ctx, client, checksumsURL, "checksums.txt")
	if err != nil {
		return err
	}
	sig, err := applyDownload(ctx, client, sigURL, "checksums.txt.sig")
	if err != nil {
		return err
	}

	archive, err := applyDownloadArchive(ctx, client, archiveURL, name)
	if err != nil {
		return err
	}

	if err := VerifyChecksums(checksums, sig, PublicKeys); err != nil {
		return err
	}
	if err := VerifyFile(checksums, name, archive); err != nil {
		return err
	}

	exe, err := filepath.EvalSymlinks(executable)
	if err != nil {
		exe = executable
	}
	dir := filepath.Dir(exe)

	tmp, err := applyExtract(archive, name, dir)
	if err != nil {
		return applyWritePermissionError(err, dir)
	}

	if err := applySmokeTest(ctx, tmp, r.Version); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	if err := replaceExecutable(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		return applyWritePermissionError(err, dir)
	}

	return nil
}

// applyWritePermissionError wraps err so its message includes "cannot
// write <dir>" when err's cause is a permission error
// (errors.Is(err, fs.ErrPermission)), leaving every other error unchanged.
func applyWritePermissionError(err error, dir string) error {
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		return err
	}
	return fmt.Errorf("update: cannot write %s: %w", dir, err)
}

// applyDownload GETs url with client, naming the asset in any error, and
// returns its body capped at applyMaxDownloadBytes.
func applyDownload(ctx context.Context, client *http.Client, url, assetName string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("update: downloading %s: %w", assetName, err)
	}
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: downloading %s: %w", assetName, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: downloading %s: unexpected status %d", assetName, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, applyMaxDownloadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("update: downloading %s: %w", assetName, err)
	}
	if len(data) > applyMaxDownloadBytes {
		return nil, fmt.Errorf("update: downloading %s: exceeds %d bytes", assetName, applyMaxDownloadBytes)
	}
	return data, nil
}

// applyDownloadArchive downloads the release archive under its own
// applyArchiveTimeout, separate from ctx's own deadline (if any).
func applyDownloadArchive(ctx context.Context, client *http.Client, url, assetName string) ([]byte, error) {
	actx, cancel := context.WithTimeout(ctx, applyArchiveTimeout)
	defer cancel()
	return applyDownload(actx, client, url, assetName)
}

// applyExtract extracts the single "ajent" (tar.gz) or "ajent.exe" (zip)
// entry from archive (named archiveName) into a new temp file created in
// dir, and returns its path. The temp file has mode 0755 on unix. On any
// error, the temp file (if created) is removed before returning.
func applyExtract(archive []byte, archiveName, dir string) (string, error) {
	isZip := strings.HasSuffix(archiveName, ".zip")
	entryName := "ajent"
	if isZip {
		entryName = "ajent.exe"
	}

	f, err := os.CreateTemp(dir, ".ajent-update-*")
	if err != nil {
		return "", fmt.Errorf("update: creating temp file in %s: %w", dir, err)
	}
	tmp := f.Name()

	var extractErr error
	if isZip {
		extractErr = applyExtractZip(archive, entryName, f)
	} else {
		extractErr = applyExtractTarGz(archive, entryName, f)
	}
	closeErr := f.Close()

	if extractErr != nil {
		_ = os.Remove(tmp)
		return "", extractErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("update: closing %s: %w", tmp, closeErr)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmp, 0o755); err != nil { //nolint:gosec // G302: tmp is the extracted ajent binary; it needs the executable bit (and world-readable, matching a normal installed binary) to run at all.
			_ = os.Remove(tmp)
			return "", fmt.Errorf("update: chmod %s: %w", tmp, err)
		}
	}

	return tmp, nil
}

// applySafeEntryName reports whether name is safe to extract: relative,
// with no ".." path segment and no leading slash. It rejects any entry
// with an unsafe name outright, whether or not that entry is the one being
// extracted, so a zip-slip attempt anywhere in the archive fails the whole
// extraction.
func applySafeEntryName(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return false
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// applyExtractTarGz scans a tar.gz archive for entryName and copies its
// contents to dst.
func applyExtractTarGz(archive []byte, entryName string, dst io.Writer) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("update: opening archive: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("update: reading archive: %w", err)
		}
		if !applySafeEntryName(hdr.Name) {
			return fmt.Errorf("update: archive entry %q has an unsafe name", hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg || hdr.Name != entryName {
			continue
		}

		n, err := io.Copy(dst, io.LimitReader(tr, applyMaxEntryBytes+1))
		if err != nil {
			return fmt.Errorf("update: extracting %q: %w", hdr.Name, err)
		}
		if n > applyMaxEntryBytes {
			return fmt.Errorf("update: archive entry %q exceeds %d bytes", hdr.Name, applyMaxEntryBytes)
		}
		found = true
	}
	if !found {
		return fmt.Errorf("update: archive has no entry %q", entryName)
	}
	return nil
}

// applyExtractZip scans a zip archive for entryName and copies its
// contents to dst.
func applyExtractZip(archive []byte, entryName string, dst io.Writer) error {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return fmt.Errorf("update: opening archive: %w", err)
	}

	found := false
	for _, zf := range zr.File {
		if !applySafeEntryName(zf.Name) {
			return fmt.Errorf("update: archive entry %q has an unsafe name", zf.Name)
		}
		if zf.FileInfo().IsDir() || zf.Name != entryName {
			continue
		}
		if zf.UncompressedSize64 > applyMaxEntryBytes {
			return fmt.Errorf("update: archive entry %q exceeds %d bytes", zf.Name, applyMaxEntryBytes)
		}

		rc, err := zf.Open()
		if err != nil {
			return fmt.Errorf("update: opening %q: %w", zf.Name, err)
		}
		n, err := io.Copy(dst, io.LimitReader(rc, applyMaxEntryBytes+1))
		_ = rc.Close()
		if err != nil {
			return fmt.Errorf("update: extracting %q: %w", zf.Name, err)
		}
		if n > applyMaxEntryBytes {
			return fmt.Errorf("update: archive entry %q exceeds %d bytes", zf.Name, applyMaxEntryBytes)
		}
		found = true
	}
	if !found {
		return fmt.Errorf("update: archive has no entry %q", entryName)
	}
	return nil
}

// applySmokeTest runs "<tmp> version --json" and requires it to report
// wantVersion, so a corrupted or non-functional extracted binary is never
// installed.
func applySmokeTest(ctx context.Context, tmp, wantVersion string) error {
	ctx, cancel := context.WithTimeout(ctx, applySelfCheckTimeout)
	defer cancel()

	// #nosec G204 -- tmp is the archive this same call just extracted and verified (VerifyChecksums/VerifyFile above); running it with a fixed "version --json" argument list is Apply's whole smoke-test step.
	out, err := exec.CommandContext(ctx, tmp, "version", "--json").Output()
	if err != nil {
		return errSelfCheck
	}

	var info struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &info); err != nil || info.Version != wantVersion {
		return errSelfCheck
	}
	return nil
}

// CleanupOld removes executable+".old", a leftover from a Windows
// rename-aside (replace_windows.go), ignoring every error including
// absence. It is a no-op on unix beyond removing any such leftover file.
func CleanupOld(executable string) {
	_ = os.Remove(executable + ".old")
}
