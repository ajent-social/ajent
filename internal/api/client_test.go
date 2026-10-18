// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ajent-social/ajent/internal/testutil/fakeserver"
)

// noSleep is injected as Client.Sleep so retry tests run instantly.
func noSleep(time.Duration) {}

func TestHeaders(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()

	t.Run("default client kind", func(t *testing.T) {
		c := &Client{Sleep: noSleep}
		var out map[string]any
		if err := c.Do(context.Background(), cred, Request{Method: "GET", Route: "/v1/me"}, &out); err != nil {
			t.Fatalf("Do: %v", err)
		}
		reqs := srv.Requests()
		last := reqs[len(reqs)-1]
		if got := last.Header.Get("X-Ajent-Client"); got != "go-cli" {
			t.Errorf("X-Ajent-Client = %q, want go-cli", got)
		}
		if got := last.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		if got := last.Header.Get("X-Ajent-Client-Version"); got == "" {
			t.Errorf("X-Ajent-Client-Version missing")
		}
		if got := last.Header.Get("User-Agent"); got == "" {
			t.Errorf("User-Agent missing")
		}
		if got := last.Header.Get("Authorization"); got != "Bearer "+cred.Token {
			t.Errorf("Authorization = %q, want Bearer %s", got, cred.Token)
		}
		if got := last.Header.Get("Idempotency-Key"); got != "" {
			t.Errorf("Idempotency-Key = %q, want empty", got)
		}
	})

	t.Run("mcp client kind", func(t *testing.T) {
		c := &Client{ClientKind: "mcp", Sleep: noSleep}
		var out map[string]any
		if err := c.Do(context.Background(), cred, Request{Method: "GET", Route: "/v1/me"}, &out); err != nil {
			t.Fatalf("Do: %v", err)
		}
		reqs := srv.Requests()
		last := reqs[len(reqs)-1]
		if got := last.Header.Get("X-Ajent-Client"); got != "mcp" {
			t.Errorf("X-Ajent-Client = %q, want mcp", got)
		}
	})

	t.Run("no authorization on signup", func(t *testing.T) {
		c := &Client{Sleep: noSleep}
		var out map[string]any
		if err := c.Do(context.Background(), cred, Request{Method: "POST", Route: "/v1/signup"}, &out); err != nil {
			t.Fatalf("Do: %v", err)
		}
		reqs := srv.Requests()
		last := reqs[len(reqs)-1]
		if got := last.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty on signup", got)
		}
	})

	t.Run("idempotency key present only when set", func(t *testing.T) {
		c := &Client{Sleep: noSleep}
		var out map[string]any
		if err := c.Do(context.Background(), cred, Request{Method: "POST", Route: "/v1/posts", Key: "k1"}, &out); err != nil {
			t.Fatalf("Do: %v", err)
		}
		reqs := srv.Requests()
		last := reqs[len(reqs)-1]
		if got := last.Header.Get("Idempotency-Key"); got != "k1" {
			t.Errorf("Idempotency-Key = %q, want k1", got)
		}
	})
}

func TestRedirectRefused(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()

	srv.Handle("GET", "/v1/redirect-me", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/v1/me", http.StatusFound)
	})

	c := &Client{Sleep: noSleep}
	err := c.Do(context.Background(), cred, Request{Method: "GET", Route: "/v1/redirect-me"}, nil)
	if err == nil {
		t.Fatal("Do() = nil, want error")
	}
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("Do() error = %v (%T), want *Error", err, err)
	}
	if apiErr.Status != http.StatusFound {
		t.Errorf("Status = %d, want %d", apiErr.Status, http.StatusFound)
	}
	if got := len(srv.Requests()); got != 1 {
		t.Errorf("len(Requests()) = %d, want 1", got)
	}
}

func TestOriginPinning(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()
	cred.Server = "https://evil.example"

	c := &Client{Sleep: noSleep}
	err := c.Do(context.Background(), cred, Request{Method: "GET", Route: "/v1/me"}, nil)
	if !errors.Is(err, errForeignServer) {
		t.Fatalf("Do() error = %v, want errForeignServer", err)
	}
	if got := len(srv.Requests()); got != 0 {
		t.Errorf("len(Requests()) = %d, want 0", got)
	}
}

func TestRetryIdempotentPost(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()

	var calls int32
	srv.Handle("POST", "/v1/posts", func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	c := &Client{Sleep: noSleep}
	var out map[string]any
	req := Request{Method: "POST", Route: "/v1/posts", Body: map[string]any{"a": 1}, Key: "k1"}
	if err := c.Do(context.Background(), cred, req, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}

	reqs := srv.Requests()
	if len(reqs) != 3 {
		t.Fatalf("len(Requests()) = %d, want 3", len(reqs))
	}
	for i, r := range reqs {
		if got := r.Header.Get("Idempotency-Key"); got != "k1" {
			t.Errorf("request %d: Idempotency-Key = %q, want k1", i, got)
		}
		if string(r.Body) != string(reqs[0].Body) {
			t.Errorf("request %d: body = %s, want %s", i, r.Body, reqs[0].Body)
		}
	}
}

func TestNoRetryUnsafe(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()

	srv.Handle("POST", "/v1/fleet", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	c := &Client{Sleep: noSleep}
	err := c.Do(context.Background(), cred, Request{Method: "POST", Route: "/v1/fleet"}, nil)
	if err == nil {
		t.Fatal("Do() = nil, want error")
	}
	if got := len(srv.Requests()); got != 1 {
		t.Errorf("len(Requests()) = %d, want 1", got)
	}
}

func TestNoRetryAuth(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			srv := fakeserver.New(t)
			cred := srv.Credential()
			srv.Handle("GET", "/v1/me", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			})

			c := &Client{Sleep: noSleep}
			err := c.Do(context.Background(), cred, Request{Method: "GET", Route: "/v1/me"}, nil)
			if err == nil {
				t.Fatal("Do() = nil, want error")
			}
			if got := len(srv.Requests()); got != 1 {
				t.Errorf("len(Requests()) = %d, want 1", got)
			}
		})
	}
}

func TestRetryAfter(t *testing.T) {
	t.Run("short retry-after retries with the injected sleep", func(t *testing.T) {
		srv := fakeserver.New(t)
		cred := srv.Credential()

		var calls int32
		srv.Handle("GET", "/v1/me", func(w http.ResponseWriter, _ *http.Request) {
			if atomic.AddInt32(&calls, 1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		})

		var slept []time.Duration
		c := &Client{Sleep: func(d time.Duration) { slept = append(slept, d) }}
		var out map[string]any
		if err := c.Do(context.Background(), cred, Request{Method: "GET", Route: "/v1/me"}, &out); err != nil {
			t.Fatalf("Do: %v", err)
		}
		if got := len(srv.Requests()); got != 2 {
			t.Fatalf("len(Requests()) = %d, want 2", got)
		}
		if len(slept) != 1 || slept[0] != 1*time.Second {
			t.Errorf("slept = %v, want [1s]", slept)
		}
	})

	t.Run("long retry-after gives up after one request", func(t *testing.T) {
		srv := fakeserver.New(t)
		cred := srv.Credential()

		srv.Handle("GET", "/v1/me", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusTooManyRequests)
		})

		c := &Client{Sleep: noSleep}
		err := c.Do(context.Background(), cred, Request{Method: "GET", Route: "/v1/me"}, nil)
		var apiErr *Error
		if !errors.As(err, &apiErr) {
			t.Fatalf("Do() error = %v, want *Error", err)
		}
		if apiErr.RetryAfter == nil || *apiErr.RetryAfter != 5 {
			t.Errorf("RetryAfter = %v, want 5", apiErr.RetryAfter)
		}
		if got := len(srv.Requests()); got != 1 {
			t.Errorf("len(Requests()) = %d, want 1", got)
		}
	})
}

func TestInvalidJSONIs502(t *testing.T) {
	srv := fakeserver.New(t)
	cred := srv.Credential()

	srv.Handle("GET", "/v1/me", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	})

	c := &Client{Sleep: noSleep}
	var out map[string]any
	err := c.Do(context.Background(), cred, Request{Method: "GET", Route: "/v1/me"}, &out)
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("Do() error = %v, want *Error", err)
	}
	if apiErr.Status != http.StatusBadGateway {
		t.Errorf("Status = %d, want %d", apiErr.Status, http.StatusBadGateway)
	}
	if got := len(srv.Requests()); got != 1 {
		t.Errorf("len(Requests()) = %d, want 1", got)
	}
}

// recordingObserver collects every Observation it is given, guarded by a
// mutex so concurrent Do calls in other tests cannot race on it.
type recordingObserver struct {
	mu  sync.Mutex
	obs []Observation
}

func (o *recordingObserver) Observe(ob Observation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.obs = append(o.obs, ob)
}

func (o *recordingObserver) all() []Observation {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]Observation, len(o.obs))
	copy(out, o.obs)
	return out
}

func TestObserverCalledOnce(t *testing.T) {
	t.Run("one observation with the final attempt count", func(t *testing.T) {
		srv := fakeserver.New(t)
		cred := srv.Credential()

		var calls int32
		srv.Handle("GET", "/v1/me", func(w http.ResponseWriter, _ *http.Request) {
			if atomic.AddInt32(&calls, 1) < 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		})

		obs := &recordingObserver{}
		c := &Client{Observer: obs, Sleep: noSleep}
		var out map[string]any
		if err := c.Do(context.Background(), cred, Request{Method: "GET", Route: "/v1/me"}, &out); err != nil {
			t.Fatalf("Do: %v", err)
		}

		got := obs.all()
		if len(got) != 1 {
			t.Fatalf("len(observations) = %d, want 1", len(got))
		}
		if got[0].Attempts != 2 {
			t.Errorf("Attempts = %d, want 2", got[0].Attempts)
		}
		if got[0].RouteTemplate != "/v1/me" {
			t.Errorf("RouteTemplate = %q, want /v1/me", got[0].RouteTemplate)
		}
		if got[0].Method != "GET" {
			t.Errorf("Method = %q, want GET", got[0].Method)
		}
	})

	t.Run("NoObserve skips the call", func(t *testing.T) {
		srv := fakeserver.New(t)
		cred := srv.Credential()

		obs := &recordingObserver{}
		c := &Client{Observer: obs, Sleep: noSleep}
		var out map[string]any
		req := Request{Method: "GET", Route: "/v1/me", NoObserve: true}
		if err := c.Do(context.Background(), cred, req, &out); err != nil {
			t.Fatalf("Do: %v", err)
		}
		if got := len(obs.all()); got != 0 {
			t.Errorf("len(observations) = %d, want 0", got)
		}
	})
}
