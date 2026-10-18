// SPDX-License-Identifier: Apache-2.0

// Command localrelease prepares local candidates; it never publishes or creates keys.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type runner func(context.Context, string, []string, string, []string) ([]byte, error)
type options struct {
	version, sha, out string
	local             bool
}
type receipt struct {
	Version     string   `json:"version"`
	Source      string   `json:"source_sha"`
	Kind        string   `json:"kind"`
	Go          string   `json:"go_toolchain"`
	Checks      []string `json:"completed_local_commands"`
	Limitations []string `json:"limitations"`
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Getenv, command); err != nil {
		fmt.Fprintln(os.Stderr, "localrelease:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, call runner) error {
	fs := flag.NewFlagSet("localrelease prepare", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // Never echo unknown flag values (which may contain secrets).
	var o options
	fs.StringVar(&o.version, "version", "", "stable version without v")
	fs.StringVar(&o.sha, "source-sha", "", "exact 40-character HEAD")
	fs.StringVar(&o.out, "out", "", "new absolute directory outside checkout")
	fs.BoolVar(&o.local, "local", false, "unsigned local candidate, never an official release")
	if len(args) == 0 || args[0] != "prepare" {
		return errors.New("usage: localrelease prepare --version VERSION --source-sha SHA --out ABS [--local]")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return errors.New("invalid flags")
	}
	if fs.NArg() != 0 || !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(o.version) || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(o.sha) || !filepath.IsAbs(o.out) {
		return errors.New("version, exact source-sha and absolute out are required")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	env := childEnv(getenv)
	root, err := call(ctx, "git", []string{"rev-parse", "--show-toplevel"}, "", env)
	if err != nil {
		return errors.New("cannot locate source checkout")
	}
	dir, err := filepath.EvalSymlinks(strings.TrimSpace(string(root)))
	if err != nil {
		return errors.New("cannot resolve source checkout")
	}
	if err := source(ctx, dir, o.sha, env, call); err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(o.out))
	if err != nil {
		return errors.New("output parent must already exist")
	}
	out := filepath.Join(parent, filepath.Base(o.out))
	rel, err := filepath.Rel(dir, out)
	if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return errors.New("output must be outside source checkout")
	}
	var key ed25519.PrivateKey
	if !o.local {
		key, err = signingKey(getenv("RELEASE_ED25519_KEY"), dir)
		if err != nil {
			return err
		}
	}
	// Exclusive creation: no existing files need backup and no prior output is replaced.
	if err := os.Mkdir(out, 0o700); err != nil {
		return fmt.Errorf("create new output directory: %w", err)
	}
	work := filepath.Join(out, ".work")
	if err := os.Mkdir(work, 0o700); err != nil {
		return err
	}
	snapshot, err := call(ctx, "git", []string{"archive", "--format=tar", o.sha}, dir, env)
	if err != nil {
		return errors.New("snapshot source failed")
	}
	if err := unpack(snapshot, work); err != nil {
		return err
	}
	if o.local {
		o.version += "-local." + o.sha[:12]
	}
	r := receipt{Version: o.version, Source: o.sha, Kind: "signed-candidate", Limitations: []string{"local evidence only; no hosted CI or GitHub attestation", "macOS unsigned and unnotarized; Windows unsigned", "archives only; no SBOM, deb/rpm, package metadata or channel updates", "review, landed main, tag and publication remain separate owner gates"}}
	if o.local {
		r.Kind = "unsigned-local-only"
	}
	toolchain, err := call(ctx, "go", []string{"version"}, work, env)
	if err != nil {
		return errors.New("local Go toolchain unavailable")
	}
	r.Go = strings.TrimSpace(string(toolchain))
	formatted, err := call(ctx, "gofmt", []string{"-l", "."}, work, env)
	if err != nil || len(bytes.TrimSpace(formatted)) != 0 {
		return errors.New("source is not gofmt clean")
	}
	for _, c := range []struct {
		name string
		args []string
	}{{"make", []string{"fmt", "lint", "test"}}, {"govulncheck", []string{"./..."}}} {
		log, checkErr := call(ctx, c.name, c.args, work, env)
		if err := os.WriteFile(filepath.Join(out, "."+c.name+".log"), log, 0o600); err != nil {
			return err
		}
		if checkErr != nil {
			return fmt.Errorf("%s failed; private diagnostic log retained", c.name)
		}
		r.Checks = append(r.Checks, c.name+" "+strings.Join(c.args, " "))
	}
	stamp, err := call(ctx, "git", []string{"show", "-s", "--format=%cI", o.sha}, dir, env)
	if err != nil {
		return errors.New("cannot read source commit timestamp")
	}
	date, err := time.Parse(time.RFC3339, strings.TrimSpace(string(stamp)))
	if err != nil {
		return errors.New("invalid commit timestamp")
	}
	var names []string
	for _, target := range []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64"} {
		parts := strings.Split(target, "/")
		bin := "ajent"
		if parts[0] == "windows" {
			bin += ".exe"
		}
		binary := filepath.Join(out, "."+parts[0]+"_"+parts[1]+"_"+bin)
		pkg := "github.com/ajent-social/ajent/internal/version."
		flags := "-s -w -X " + pkg + "Version=" + o.version + " -X " + pkg + "Commit=" + o.sha + " -X " + pkg + "Date=" + date.UTC().Format(time.RFC3339)
		buildEnv := append(append([]string{}, env...), "GOOS="+parts[0], "GOARCH="+parts[1], "CGO_ENABLED=0")
		log, buildErr := call(ctx, "go", []string{"build", "-mod=readonly", "-trimpath", "-ldflags", flags, "-o", binary, "./cmd/ajent"}, work, buildEnv)
		if buildErr != nil {
			if err := os.WriteFile(filepath.Join(out, ".build.log"), log, 0o600); err != nil {
				return err
			}
			return fmt.Errorf("build %s failed; private diagnostic log retained", target)
		}
		name := "ajent_" + o.version + "_" + parts[0] + "_" + parts[1] + ".tar.gz"
		if parts[0] == "windows" {
			name = strings.TrimSuffix(name, ".tar.gz") + ".zip"
		}
		if err := archive(filepath.Join(out, name), work, binary, bin, date); err != nil {
			return err
		}
		names = append(names, name)
	}
	native := filepath.Join(out, "."+runtime.GOOS+"_"+runtime.GOARCH+"_ajent")
	if runtime.GOOS == "windows" {
		native += ".exe"
	}
	info, err := call(ctx, native, []string{"version", "--json"}, work, env)
	if err != nil {
		return errors.New("native candidate self-check failed")
	}
	var v struct{ Version, Commit string }
	if json.Unmarshal(info, &v) != nil || v.Version != o.version || v.Commit != o.sha {
		return errors.New("native candidate metadata mismatch")
	}
	r.Checks = append(r.Checks, "native version --json (version and commit matched)")
	if err := source(ctx, dir, o.sha, env, call); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(out, "verification.json"), r); err != nil {
		return err
	}
	names = append(names, "verification.json")
	var checks bytes.Buffer
	for _, name := range names {
		// #nosec G304 -- names are fixed artifact basenames created above in the caller-selected new private output directory.
		data, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			return err
		}
		fmt.Fprintf(&checks, "%x  %s\n", sha256.Sum256(data), name)
	}
	if err := os.WriteFile(filepath.Join(out, "checksums.txt"), checks.Bytes(), 0o600); err != nil {
		return err
	}
	if !o.local {
		sig := ed25519.Sign(key, checks.Bytes())
		if !ed25519.Verify(key.Public().(ed25519.PublicKey), checks.Bytes(), sig) {
			return errors.New("signature self-check failed")
		}
		if err := os.WriteFile(filepath.Join(out, "checksums.txt.sig"), []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o600); err != nil {
			return err
		}
		for _, name := range names {
			if _, err := call(ctx, native, []string{"verify-release", "--checksums", filepath.Join(out, "checksums.txt"), "--sig", filepath.Join(out, "checksums.txt.sig"), "--file", filepath.Join(out, name)}, work, env); err != nil {
				return errors.New("native embedded-key verification failed")
			}
		}
	}
	// The final marker means preparation only, never permission to publish.
	return writeJSON(filepath.Join(out, "prepared.json"), struct{ Kind, Source, ChecksumsSHA256 string }{r.Kind, o.sha, fmt.Sprintf("%x", sha256.Sum256(checks.Bytes()))})
}

func command(ctx context.Context, name string, args []string, dir string, env []string) ([]byte, error) {
	// #nosec G204 -- executable and arguments are fixed maintainer build steps, never remote input.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = dir, env
	return cmd.CombinedOutput()
}

func childEnv(getenv func(string) string) []string {
	var env []string
	for _, k := range []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "SYSTEMROOT", "WINDIR", "GOPATH", "GOCACHE", "GOMODCACHE", "GOTMPDIR", "GOMAXPROCS", "CC", "CXX"} {
		if v := getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return append(env, "CGO_ENABLED=1", "GOENV=off", "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local", "GOPROXY=off", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
}

func source(ctx context.Context, dir, sha string, env []string, call runner) error {
	head, err := call(ctx, "git", []string{"rev-parse", "HEAD"}, dir, env)
	if err != nil || strings.TrimSpace(string(head)) != sha {
		return errors.New("source SHA does not match HEAD")
	}
	status, err := call(ctx, "git", []string{"status", "--porcelain", "--untracked-files=all"}, dir, env)
	if err != nil || len(bytes.TrimSpace(status)) != 0 {
		return errors.New("source checkout must be clean, including untracked files")
	}
	return nil
}

func signingKey(raw, dir string) (ed25519.PrivateKey, error) {
	bad := errors.New("approved RELEASE_ED25519_KEY must match source releaseKeys and packaging public PEM")
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || (len(decoded) != 32 && len(decoded) != 64) {
		return nil, bad
	}
	key := ed25519.NewKeyFromSeed(decoded[:32])
	if len(decoded) == 64 && !bytes.Equal(decoded, key) {
		return nil, bad
	}
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, "internal/update/keys.go"), nil, 0)
	if err != nil {
		return nil, bad
	}
	matched := false
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "releaseKeys" || len(spec.Values) != 1 {
			return true
		}
		list, ok := spec.Values[0].(*ast.CompositeLit)
		if !ok {
			return false
		}
		for _, e := range list.Elts {
			lit, ok := e.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			pub, err := base64.StdEncoding.DecodeString(s)
			if err == nil && bytes.Equal(pub, key.Public().(ed25519.PublicKey)) {
				matched = true
			}
		}
		return false
	})
	// #nosec G304 -- fixed public PEM path under the verified, caller-selected source checkout; no private key is read.
	data, err := os.ReadFile(filepath.Join(dir, "packaging/ajent-release.pub.pem"))
	if err != nil || !matched {
		return nil, bad
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, bad
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	p, ok := pub.(ed25519.PublicKey)
	if err != nil || !ok || !bytes.Equal(p, key.Public().(ed25519.PublicKey)) {
		return nil, bad
	}
	return key, nil
}

const (
	maxSourceEntry int64 = 128 << 20
	maxSourceTotal int64 = 1 << 30
)

func addSourceSize(total, size int64) (int64, error) {
	if size < 0 || size > maxSourceEntry || total < 0 || total > maxSourceTotal-size {
		return 0, errors.New("source archive exceeds entry or total size limit")
	}
	return total + size, nil
}

func unpack(data []byte, dir string) error {
	tr := tar.NewReader(bytes.NewReader(data))
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		total, err = addSourceSize(total, h.Size)
		if err != nil {
			return err
		}
		name := filepath.FromSlash(h.Name)
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(filepath.Clean(name), ".."+string(filepath.Separator)) {
			return errors.New("unsafe source archive path")
		}
		path := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeXGlobalHeader:
			// git archive carries the commit ID in a global PAX header.
			continue
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			// #nosec G304 -- path is a validated local git-archive entry under the new private snapshot directory; exclusive creation refuses replacement.
			f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(h.Mode&0o700))
			if err != nil {
				return err
			}
			copied, copyErr := io.CopyN(f, tr, h.Size)
			if copied != h.Size {
				copyErr = errors.Join(copyErr, io.ErrUnexpectedEOF)
			}
			closeErr := f.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				return err
			}
		default:
			return errors.New("source snapshot contains unsupported link or file type")
		}
	}
}

func archive(dest, sourceDir, binary, binName string, date time.Time) error {
	// #nosec G304 -- dest is a fixed generated archive name under the new private output directory; exclusive creation refuses replacement.
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	var zw *zip.Writer
	var gz *gzip.Writer
	var tw *tar.Writer
	if strings.HasSuffix(dest, ".zip") {
		zw = zip.NewWriter(f)
	} else {
		gz = gzip.NewWriter(f)
		tw = tar.NewWriter(gz)
	}
	var result error
	for _, name := range []string{binName, "LICENSE", "NOTICE", "README.md"} {
		path := filepath.Join(sourceDir, name)
		mode := os.FileMode(0o644)
		if name == binName {
			path = binary
			mode = 0o755
		}
		// #nosec G304 -- path selects only the freshly built binary or fixed tracked LICENSE, NOTICE and README within the private source snapshot.
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			result = readErr
			break
		}
		var w io.Writer
		if zw != nil {
			h := &zip.FileHeader{Name: name, Method: zip.Deflate}
			h.SetMode(mode)
			h.Modified = date
			w, err = zw.CreateHeader(h)
		} else {
			err = tw.WriteHeader(&tar.Header{Name: name, Mode: int64(mode), Size: int64(len(data)), ModTime: date})
			w = tw
		}
		if err != nil {
			result = err
			break
		}
		if _, err = w.Write(data); err != nil {
			result = err
			break
		}
	}
	if zw != nil {
		result = errors.Join(result, zw.Close())
	} else {
		result = errors.Join(result, tw.Close(), gz.Close())
	}
	return errors.Join(result, f.Close())
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
