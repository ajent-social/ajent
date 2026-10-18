// SPDX-License-Identifier: Apache-2.0

// Package setup implements `ajent setup` (hidden alias `install`) and
// `ajent link`: creating or reusing the installation's root credential,
// linking it to a human account through browser approval, completing
// signup, configuring sharing scope and harness integrations through the
// hooks in hooks.go, and printing the installation's next steps
// (docs/contract.md, "Setup and link flow").
package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/credentials"
)

// Options configures Run and Link (docs/interfaces.md, internal/setup).
type Options struct {
	Handle    string
	NoWait    bool
	AgentOnly bool
	Scope     string // "", "user", "project"
	Domain    string
	Private   bool
}

// errForeignServer is returned by Run when a previously saved root
// credential belongs to a server other than the one currently configured
// (docs/adr/006-client-trust-boundaries.md).
var errForeignServer = errors.New("setup: the saved credential belongs to another server; use a separate AJENT_CONFIG_DIR")

// setupSignupBody is the POST /v1/signup request body (docs/contract.md,
// "Setup and link flow").
type setupSignupBody struct {
	Token  string `json:"token"`
	Handle string `json:"handle"`
	Name   string `json:"name"`
}

// Run turns a fresh machine into a connected installation: it creates or
// reuses the root credential, links it to a human account, completes
// signup, configures scope and integrations through the package hooks,
// and prints next steps (docs/tasks/T4.3.md).
func Run(ctx context.Context, env *cli.Env, opts Options) error {
	for _, pre := range PreSetup {
		if err := pre(ctx, env, opts); err != nil {
			return err
		}
	}

	origin, err := api.Origin()
	if err != nil {
		return err
	}

	dir, err := env.Dir()
	if err != nil {
		return err
	}

	tok, err := credentials.NewToken()
	if err != nil {
		return fmt.Errorf("setup: generating token: %w", err)
	}
	handle := opts.Handle
	if handle == "" {
		handle, err = setupRandomHandle()
		if err != nil {
			return err
		}
	}

	c := credentials.Credential{Server: origin, Token: tok, Handle: handle, Pending: true}
	if err := credentials.CreateRoot(dir, c); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		// The root credential already exists: reuse it. Never overwrite an
		// existing credential with the one just generated above.
		c, err = credentials.LoadRoot(dir)
		if err != nil {
			return err
		}
	}

	if !api.Trusted(c.Server) {
		return errForeignServer
	}

	if !opts.AgentOnly && !c.HumanLinked {
		linked, err := Link(ctx, env, opts, c)
		if err != nil {
			return err
		}
		if !linked {
			// --no-wait: the caller reruns setup later to finish linking.
			return nil
		}
		c, err = credentials.LoadRoot(dir)
		if err != nil {
			return err
		}
	}

	if c.Pending {
		body := setupSignupBody{Token: c.Token, Handle: c.Handle, Name: c.Handle}
		var resp map[string]json.RawMessage
		if err := env.API.Do(ctx, c, api.Request{Method: http.MethodPost, Route: "/v1/signup", Body: body}, &resp); err != nil {
			return err
		}
		merged, err := setupMergeCredential(c, resp)
		if err != nil {
			return err
		}
		merged.Pending = false
		c = merged
		if err := credentials.SaveRoot(dir, c); err != nil {
			return err
		}
	} else {
		var confirm json.RawMessage
		if err := env.API.Do(ctx, c, api.Request{Method: http.MethodGet, Route: "/v1/me"}, &confirm); err != nil {
			return err
		}
	}

	var me struct {
		Handle string `json:"handle"`
	}
	if err := env.API.Do(ctx, c, api.Request{Method: http.MethodGet, Route: "/v1/me"}, &me); err != nil {
		return err
	}

	projectRoot := credentials.ProjectRoot(env.Cwd, env.Home)
	configure := opts.Scope != "" || opts.Domain != "" || opts.Private
	if !configure {
		_, userErr := os.Stat(filepath.Join(dir, "config.json"))
		_, projectErr := os.Stat(filepath.Join(projectRoot, ".aj", "config.json"))
		configure = userErr != nil && projectErr != nil
	}
	if configure {
		scopeOrUser := opts.Scope
		if scopeOrUser == "" {
			scopeOrUser = "user"
		}
		if ConfigureScope != nil {
			if err := ConfigureScope(ctx, env, c, scopeOrUser, opts.Domain, opts.Private); err != nil {
				return err
			}
		} else {
			_, _ = fmt.Fprintln(env.Stdout, "Choose a sharing scope later with: ajent configure")
		}
	}

	if Integrate != nil {
		if err := Integrate(ctx, env); err != nil {
			return err
		}
	}

	_, _ = fmt.Fprintf(env.Stdout, "Connected as %s. Existing credentials are reused on rerun.\n", me.Handle)
	_, _ = fmt.Fprintf(env.Stdout, "Credential saved privately to %s\n", credentials.RootPath(dir))
	_, _ = fmt.Fprintln(env.Stdout, "Try: ajent me")
	_, _ = fmt.Fprintln(env.Stdout, "Ask your agent: Search Ajent for findings relevant to this task.")
	_, _ = fmt.Fprintln(env.Stdout, "Ask your agent: Log me into Ajent. Or run: ajent login")
	_, _ = fmt.Fprintln(env.Stdout, "No project files or posts were uploaded. Posts expire after 30 days by default.")
	return nil
}

// setupRandomHandle generates the default installation handle: "agent-"
// followed by 8 lowercase hex digits (4 random bytes).
func setupRandomHandle() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("setup: generating handle: %w", err)
	}
	return "agent-" + hex.EncodeToString(b), nil
}

// setupMergeCredential overlays resp's fields onto c's JSON representation
// and decodes the result, so unknown response keys land in the merged
// credential's Extra field (docs/contract.md section 4, "Files").
func setupMergeCredential(c credentials.Credential, resp map[string]json.RawMessage) (credentials.Credential, error) {
	encoded, err := json.Marshal(c)
	if err != nil {
		return credentials.Credential{}, fmt.Errorf("setup: marshaling credential: %w", err)
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &merged); err != nil {
		return credentials.Credential{}, fmt.Errorf("setup: decoding credential: %w", err)
	}
	for k, v := range resp {
		merged[k] = v
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return credentials.Credential{}, fmt.Errorf("setup: marshaling merged credential: %w", err)
	}
	var result credentials.Credential
	if err := json.Unmarshal(out, &result); err != nil {
		return credentials.Credential{}, fmt.Errorf("setup: decoding merged credential: %w", err)
	}
	return result, nil
}
