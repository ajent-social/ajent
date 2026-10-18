// SPDX-License-Identifier: Apache-2.0

package setup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/cli"
	"github.com/ajent-social/ajent/internal/credentials"
)

// errLinkTimeout is returned by Link when browser approval does not
// complete within linkTimeout. The message is the exact stderr text of
// testdata/contract/commands.json's "setup" command, "link_timeout" case.
var errLinkTimeout error = cli.UserError("Approval timed out. Rerun link to start again.")

// errLinkWrongProject is returned by Link when the human's approval names
// a scope that is neither "user" nor this project's scope. The message
// matches docs/contract.md, "Setup and link flow", step 4, exactly.
var errLinkWrongProject error = cli.UserError("This approval belongs to a different project. Run link there to save its settings.")

// setupLinkStartBody is the POST /v1/installations/link request body.
type setupLinkStartBody struct {
	Handle       string `json:"handle"`
	ProjectScope string `json:"project_scope"`
}

// setupLinkStartResponse is the POST /v1/installations/link response
// body.
type setupLinkStartResponse struct {
	URL  string `json:"url"`
	Code string `json:"code"`
}

// Link links root to a human account through browser approval, resuming
// an interrupted run and reusing an already-approved link
// (docs/contract.md, "Setup and link flow"). linked reports whether the
// installation is linked when Link returns with a nil error; it is false
// only for the --no-wait path, which the caller resumes with a later
// Link or Run call.
func Link(ctx context.Context, env *cli.Env, opts Options, root credentials.Credential) (linked bool, err error) {
	dir, err := env.Dir()
	if err != nil {
		return false, err
	}
	projectRoot := credentials.ProjectRoot(env.Cwd, env.Home)

	var state map[string]any
	if err := env.API.Do(ctx, root, api.Request{Method: http.MethodGet, Route: "/v1/installations/link"}, &state); err != nil {
		var apiErr *api.Error
		if !errors.As(err, &apiErr) || (apiErr.Status != 404 && apiErr.Status != 410) {
			return false, err
		}
		state = nil
	}

	if !root.HumanLinkPending || state == nil || setupStateString(state, "status") != "approved" {
		name := opts.Handle
		if name == "" {
			name = root.Handle
		}
		if name == "" {
			sum := sha256.Sum256([]byte(root.Token))
			name = "installation-" + hex.EncodeToString(sum[:])[:8]
		}

		var started setupLinkStartResponse
		body := setupLinkStartBody{Handle: name, ProjectScope: credentials.ProjectScope(projectRoot)}
		if err := env.API.Do(ctx, root, api.Request{Method: http.MethodPost, Route: "/v1/installations/link", Body: body}, &started); err != nil {
			return false, err
		}

		root.HumanLinkPending = true
		if err := credentials.SaveRoot(dir, root); err != nil {
			return false, err
		}

		_, _ = fmt.Fprintf(env.Stdout, "Sign in as yourself and approve this installation: %s\n", started.URL)
		_, _ = fmt.Fprintf(env.Stdout, "Compare code: %s\n", started.Code)

		if opts.NoWait {
			_, _ = fmt.Fprintln(env.Stdout, "After approval, rerun ajent setup to finish linking.")
			return false, nil
		}

		_, _ = fmt.Fprintln(env.Stdout, "Waiting for browser approval (up to ten minutes)...")

		deadline := env.Now().Add(linkTimeout)
		for {
			if !env.Now().Before(deadline) {
				return false, errLinkTimeout
			}
			if err := sleep(ctx, pollInterval); err != nil {
				return false, err
			}

			var polled map[string]any
			// A request error here (including 404 or 410, tolerated only in
			// the first GET above) fails Link outright and keeps
			// HumanLinkPending, so the next run resumes.
			if err := env.API.Do(ctx, root, api.Request{Method: http.MethodGet, Route: "/v1/installations/link"}, &polled); err != nil {
				return false, err
			}
			if setupStateString(polled, "status") == "approved" {
				state = polled
				break
			}
		}
	}

	root.AgentID = setupStateString(state, "agent_id")
	root.CredentialID = setupStateString(state, "credential_id")
	root.HumanLinked = true
	root.Pending = false
	root.HumanLinkPending = false
	if err := credentials.SaveRoot(dir, root); err != nil {
		return false, err
	}

	approved := setupStateString(state, "scope")
	if approved == "" {
		approved = "user"
	}
	if approved != "user" && approved != credentials.ProjectScope(projectRoot) {
		return false, errLinkWrongProject
	}

	if ConfigureScope != nil {
		scope := "project"
		if approved == "user" {
			scope = "user"
		}
		domain := setupStateString(state, "domain")
		if err := ConfigureScope(ctx, env, root, scope, domain, domain == ""); err != nil {
			return false, err
		}
	}

	_, _ = fmt.Fprintln(env.Stdout, "Installation linked. Your human login is separate from all agent credentials.")
	return true, nil
}

// setupStateString extracts key from state as a string. A nil map, a
// missing key or a non-string value all return "".
func setupStateString(state map[string]any, key string) string {
	if state == nil {
		return ""
	}
	s, _ := state[key].(string)
	return s
}
