// SPDX-License-Identifier: Apache-2.0

// Package scope configures the sharing scope (all your projects, or one
// project) and the domain or private mode ajent uses to represent an
// installation (docs/contract.md section 4, ADR 006 "Repository content
// grants nothing"). It backs the "ajent configure" CLI command
// (cmd/ajent/cmd_scope.go) and is reused non-interactively by "ajent setup"
// (T4.3, through T2.13's assignment of setup.ConfigureScope).
package scope

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/config"
	"github.com/ajent-social/ajent/internal/credentials"
)

// Options selects the sharing scope Configure applies.
type Options struct {
	// Scope is "", "user" or "project". An empty value asks interactively,
	// is deferred (DuringSetup) in a non-interactive session, or is
	// otherwise rejected.
	Scope string
	// Domain is the domain to represent. It is read only when DomainSet is
	// true.
	Domain string
	// DomainSet is true when --domain was given on the command line, even
	// as an empty string: it distinguishes "no flag" from `--domain=""`.
	DomainSet bool
	// Private selects private mode instead of a domain.
	Private bool
	// DuringSetup makes a missing scope or domain choice, in a
	// non-interactive session, return ErrDeferred instead of failing.
	DuringSetup bool
}

// ErrDeferred is returned by Configure when a non-interactive setup leaves
// the domain choice unmade; the installation stays private until a human
// runs "ajent configure" later.
var ErrDeferred = errors.New("scope: connected privately; configure a domain later with ajent configure --scope user|project --domain DOMAIN")

// The messages below marked cli.UserError are printed to the user verbatim
// (docs/contract.md section 8): they are the exact "stderr" text of the
// matching case in testdata/contract/commands.json for "configure", so Go's
// lowercase error-string convention does not apply to them. Messages with
// no matching fixture case stay plain, lowercase sentinel errors.
var (
	// errNoScope: configure case "no_scope".
	errNoScope error = cli.UserError("Choose --scope user or --scope project.")
	// errBadChoice has no fixture case; it is unique to the interactive
	// scope prompt.
	errBadChoice = errors.New("choose 1 or 2")
	// errNoDomainChoice: configure case "no_domain".
	errNoDomainChoice error = cli.UserError("Choose --domain DOMAIN or --private.")
	// errBadDomain: configure case "bad_domain".
	errBadDomain error = cli.UserError("Enter a DNS domain without a URL or port.")
	// errNeedsApproval: configure case "needs_approval".
	errNeedsApproval error = cli.UserError("This scope needs human approval. Run ajent link, sign in and approve the domain scope.")
	// errNoAnswer has no fixture case; it is unique to the prompt helper.
	errNoAnswer = errors.New("no answer received; use --scope and --domain or --private")
)

// domainPattern is Go's equivalent of the reference lookahead-based regex.
// Go's regexp has no lookahead, so ValidDomain checks the total length (1
// to 253 bytes) separately before matching this pattern.
var domainPattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidDomain reports whether d is a plain DNS domain after normalization:
// no scheme, no port, at least one dot, and only letters, digits and
// hyphens in each label.
func ValidDomain(d string) bool {
	d = normalizeDomain(d)
	if len(d) < 1 || len(d) > 253 {
		return false
	}
	return domainPattern.MatchString(d)
}

// normalizeDomain lowercases d, trims surrounding whitespace, and trims one
// trailing dot.
func normalizeDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	return strings.TrimSuffix(d, ".")
}

// domainEntry is one element of the GET /v1/domains "domains" array.
type domainEntry struct {
	Domain   string `json:"domain"`
	Verified bool   `json:"verified"`
}

// fetchDomains issues GET /v1/domains with root and returns the domains
// list.
func fetchDomains(ctx context.Context, env *cli.Env, root credentials.Credential) ([]domainEntry, error) {
	var resp struct {
		Domains []domainEntry `json:"domains"`
	}
	if err := env.API.Do(ctx, root, api.Request{Method: http.MethodGet, Route: "/v1/domains"}, &resp); err != nil {
		return nil, err
	}
	return resp.Domains, nil
}

// verifiedDomainNames returns the verified domain names from entries, in
// their given order.
func verifiedDomainNames(entries []domainEntry) []string {
	var out []string
	for _, e := range entries {
		if e.Verified {
			out = append(out, e.Domain)
		}
	}
	return out
}

// Configure asks for (interactive) or applies (flags) the sharing scope,
// requests a domain grant when needed, and writes config.json or
// .aj/config.json and scope-grants.json. It returns a one-line message for
// stdout.
func Configure(ctx context.Context, env *cli.Env, root credentials.Credential, opts Options) (string, error) {
	tty, ttyErr := env.OpenTTY()
	interactive := ttyErr == nil
	if interactive {
		defer func() { _ = tty.Close() }()
	}

	scopeChoice := opts.Scope
	if scopeChoice == "" {
		if !interactive {
			if opts.DuringSetup {
				return "", ErrDeferred
			}
			return "", errNoScope
		}
		answer, err := prompt(tty, "Use Ajent for (1) all your projects or (2) this project?", "1")
		if err != nil {
			return "", err
		}
		switch answer {
		case "1":
			scopeChoice = "user"
		case "2":
			scopeChoice = "project"
		default:
			return "", errBadChoice
		}
	}

	key := "user"
	if scopeChoice != "user" {
		key = credentials.ProjectScope(credentials.ProjectRoot(env.Cwd, env.Home))
	}

	domain := opts.Domain
	private := opts.Private

	if !opts.DomainSet && !private {
		entries, err := fetchDomains(ctx, env, root)
		if err != nil {
			return "", err
		}
		if !interactive {
			if opts.DuringSetup {
				return "", ErrDeferred
			}
			return "", errNoDomainChoice
		}

		verified := verifiedDomainNames(entries)
		if len(verified) > 0 {
			if _, err := io.WriteString(tty, "Verified domains: "+strings.Join(verified, ", ")+"\n"); err != nil {
				return "", fmt.Errorf("scope: writing verified domains: %w", err)
			}
		}
		def := "private"
		if len(verified) == 1 {
			def = verified[0]
		}
		answer, err := prompt(tty, "Domain to represent (or private)", def)
		if err != nil {
			return "", err
		}
		if answer == "private" {
			private = true
		} else {
			domain = answer
		}
	}

	var grant map[string]any
	if !private {
		domain = normalizeDomain(domain)
		if !ValidDomain(domain) {
			return "", errBadDomain
		}

		entries, err := fetchDomains(ctx, env, root)
		if err != nil {
			return "", err
		}
		verifiedAlready := false
		for _, e := range entries {
			if e.Domain == domain && e.Verified {
				verifiedAlready = true
				break
			}
		}
		if !verifiedAlready {
			var proof struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			}
			challenge := api.Request{
				Method: http.MethodPost,
				Route:  "/v1/domains/proof",
				Body:   map[string]any{"domain": domain, "action": "challenge"},
			}
			if err := env.API.Do(ctx, root, challenge, &proof); err != nil {
				return "", err
			}
			if _, err := fmt.Fprintf(env.Stdout, "Add DNS TXT %s = %s\n", proof.Name, proof.Value); err != nil {
				return "", fmt.Errorf("scope: writing proof notice: %w", err)
			}
			if interactive {
				if _, err := prompt(tty, "Press Enter when the DNS record is ready", ""); err != nil {
					return "", err
				}
			}
			check := api.Request{
				Method: http.MethodPost,
				Route:  "/v1/domains/proof",
				Body:   map[string]any{"domain": domain, "action": "check"},
			}
			if err := env.API.Do(ctx, root, check, nil); err != nil {
				return "", err
			}
		}

		grantReq := api.Request{
			Method: http.MethodPost,
			Route:  "/v1/domain-grants",
			Body:   map[string]any{"domain": domain, "scope": key},
		}
		var g map[string]any
		if err := env.API.Do(ctx, root, grantReq, &g); err != nil {
			var apiErr *api.Error
			if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden {
				return "", errNeedsApproval
			}
			return "", err
		}
		grant = g
	}

	dir, err := env.Dir()
	if err != nil {
		return "", err
	}

	var target string
	if scopeChoice == "user" {
		target = filepath.Join(dir, "config.json")
	} else {
		projectDir := filepath.Join(credentials.ProjectRoot(env.Cwd, env.Home), ".aj")
		if err := config.EnsurePrivateDir(projectDir); err != nil {
			return "", err
		}
		target = filepath.Join(projectDir, "config.json")
	}

	settings, err := readSettingsObject(target)
	if err != nil {
		return "", err
	}
	settings["version"] = 1
	if private {
		settings["domain"] = ""
	} else {
		settings["domain"] = domain
	}
	settings["private_only"] = private

	if grant != nil {
		if err := saveGrant(dir, root, key, domain, grant); err != nil {
			return "", err
		}
	}

	if err := config.WriteJSON(target, settings); err != nil {
		return "", err
	}

	label := "Private mode"
	if !private {
		label = domain
	}
	scopeLabel := "all your projects"
	if scopeChoice != "user" {
		scopeLabel = "this project"
	}
	return fmt.Sprintf("%s saved for %s. New and existing agents inherit these settings on their next operation.", label, scopeLabel), nil
}

// readSettingsObject reads path as a JSON object, returning an empty map
// when the file does not exist. A file that decodes to anything other than
// a JSON object is an error.
func readSettingsObject(path string) (map[string]any, error) {
	data, err := config.ReadPrivate(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("scope: %s: invalid JSON: %w", path, err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("scope: %s: not a JSON object", path)
	}
	return m, nil
}

// saveGrant records grant under bindings[root.Identity()][key] in
// <dir>/scope-grants.json, replacing any existing entry for the same
// domain, under an exclusive lock on <dir>/scope-grants.lock.
func saveGrant(dir string, root credentials.Credential, key, domain string, grant map[string]any) error {
	lock, err := config.Acquire(filepath.Join(dir, "scope-grants.lock"), 5*time.Second)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	path := filepath.Join(dir, "scope-grants.json")
	bindings := map[string]map[string][]map[string]any{}
	data, err := config.ReadPrivate(path)
	switch {
	case err == nil:
		if unmarshalErr := json.Unmarshal(data, &bindings); unmarshalErr != nil {
			return fmt.Errorf("scope: %s: invalid JSON: %w", path, unmarshalErr)
		}
	case errors.Is(err, fs.ErrNotExist):
		// Missing means no bindings yet.
	default:
		return err
	}

	identity := root.Identity()
	account := bindings[identity]
	if account == nil {
		account = map[string][]map[string]any{}
	}
	kept := make([]map[string]any, 0, len(account[key])+1)
	for _, g := range account[key] {
		if existing, _ := g["domain"].(string); existing != domain {
			kept = append(kept, g)
		}
	}
	account[key] = append(kept, grant)
	bindings[identity] = account

	return config.WriteJSON(path, bindings)
}

// prompt writes msg to tty, followed by " [def]" when def is non-empty and
// a trailing ":", then reads one line of the answer with a fresh
// bufio.Reader. An empty (trimmed) answer becomes def. Reaching EOF with no
// text at all returns errNoAnswer.
func prompt(tty io.ReadWriter, msg, def string) (string, error) {
	text := msg
	if def != "" {
		text += " [" + def + "]"
	}
	text += ":"
	if _, err := io.WriteString(tty, text); err != nil {
		return "", fmt.Errorf("scope: writing prompt: %w", err)
	}

	line, err := bufio.NewReader(tty).ReadString('\n')
	if err != nil {
		if !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("scope: reading answer: %w", err)
		}
		if line == "" {
			return "", errNoAnswer
		}
	}

	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}
