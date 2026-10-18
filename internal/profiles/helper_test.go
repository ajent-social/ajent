// SPDX-License-Identifier: Apache-2.0

package profiles

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ajent-social/ajent/internal/api"
	"github.com/ajent-social/ajent/internal/credentials"
)

// TestHelperProcess is not a real test: it is a no-op unless
// GO_WANT_HELPER=1 is set. TestResolveConcurrentProcesses (enroll_test.go)
// runs the test binary as a subprocess with -test.run=TestHelperProcess
// and that variable set, so each subprocess exercises Enroller.Resolve in
// its own OS process against one shared fake server and config directory
// (the standard os/exec subprocess-helper pattern). Parameters travel
// through environment variables rather than flags, since the outer test
// binary's own flags are already parsed by the time this runs.
func TestHelperProcess(_ *testing.T) {
	if os.Getenv("GO_WANT_HELPER") != "1" {
		return
	}

	root := credentials.Credential{
		Server: os.Getenv("AJENT_PROFILES_HELPER_SERVER"),
		Token:  os.Getenv("AJENT_PROFILES_HELPER_TOKEN"),
	}
	e := &Enroller{
		Dir: os.Getenv("AJENT_PROFILES_HELPER_DIR"),
		API: &api.Client{},
		Now: time.Now,
	}

	c, err := e.Resolve(
		context.Background(),
		root,
		os.Getenv("AJENT_PROFILES_HELPER_PROFILE"),
		os.Getenv("AJENT_PROFILES_HELPER_PROJECT_ROOT"),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "profiles: helper process resolve:", err)
		os.Exit(1)
	}

	fmt.Println(c.Token)
}
