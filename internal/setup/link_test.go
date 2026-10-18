// SPDX-License-Identifier: Apache-2.0

package setup

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

func TestLinkWrongProjectScope(t *testing.T) {
	srv := fakeserver.New(t)
	env, _, _ := setupTestEnv(t)

	srv.Handle(http.MethodGet, "/v1/installations/link", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"approved","agent_id":"00000000-0000-4000-8000-000000000001","credential_id":"00000000-0000-4000-8000-000000000002","scope":"project:deadbeef"}`))
	})

	root := srv.Credential()
	root.HumanLinkPending = true

	linked, err := Link(context.Background(), env, Options{}, root)
	if linked {
		t.Error("linked = true, want false")
	}
	if !errors.Is(err, errLinkWrongProject) {
		t.Fatalf("Link error = %v, want errLinkWrongProject", err)
	}
	// Exact wording from docs/contract.md, "Setup and link flow", step 4.
	if err.Error() != "This approval belongs to a different project. Run link there to save its settings." {
		t.Errorf("error text = %q", err.Error())
	}
}
