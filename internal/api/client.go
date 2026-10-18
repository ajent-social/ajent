// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ajent-social/ajent/internal/credentials"
	"github.com/ajent-social/ajent/internal/version"
)

// defaultTimeout is used when Request.Timeout is zero.
const defaultTimeout = 10 * time.Second

// defaultAttempts is used when Request.Attempts is zero.
const defaultAttempts = 3

// maxRetryAfterWaitS is the largest Retry-After value (in seconds) that
// still allows a retry (docs/contract.md section 7).
const maxRetryAfterWaitS = 2

// Client sends requests to the hosted service.
type Client struct {
	// HTTP is the underlying HTTP client. nil means a default client with
	// the request timeout and redirects refused.
	HTTP *http.Client
	// Observer, when non-nil, is notified after every logical Do call that
	// does not set Request.NoObserve.
	Observer Observer
	// Sleep is used to wait between retry attempts. nil means time.Sleep.
	Sleep func(time.Duration)
	// ClientKind is sent as X-Ajent-Client. "" falls back to
	// version.ClientKind ("go-cli"). The MCP server sets "mcp".
	ClientKind string
}

// Request describes one logical request, retried according to the
// retry-safety allowlist.
type Request struct {
	Method  string
	Route   string // "/v1/..." including query string
	Body    any    // nil = no body
	Key     string // Idempotency-Key, "" = none
	Timeout time.Duration
	// Attempts is the maximum number of attempts for a retry-safe request.
	// 0 means defaultAttempts. An unsafe request always gets one attempt.
	Attempts int
	// NoObserve, when true, skips the Client.Observer call (capability
	// probes, feedback submissions).
	NoObserve bool
}

// errForeignServer is returned by Do when the credential's server is not
// the configured, trusted origin.
var errForeignServer = errors.New("api: credential belongs to another server; use a separate AJENT_CREDENTIAL_FILE")

// redirectRefusedError represents a 3xx response intercepted by
// redirectRefusingTransport before it reached http.Client's
// redirect-following logic.
type redirectRefusedError struct {
	status int
	header http.Header
}

func (e *redirectRefusedError) Error() string {
	return fmt.Sprintf("api: redirect refused (status %d)", e.status)
}

// redirectRefusingTransport wraps an http.RoundTripper so that any 3xx
// response is turned into a redirectRefusedError instead of being followed,
// even when the wrapped client's CheckRedirect is not itself set to refuse
// redirects (for example an http.Client injected by a test).
type redirectRefusingTransport struct {
	base http.RoundTripper
}

func (t *redirectRefusingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		header := resp.Header.Clone()
		status := resp.StatusCode
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return nil, &redirectRefusedError{status: status, header: header}
	}
	return resp, nil
}

// httpClient returns the HTTP client to use, always wrapped so redirects
// are refused (step 4 of docs/tasks/T2.4.md).
func (c *Client) httpClient(timeout time.Duration) *http.Client {
	if c.HTTP == nil {
		return &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &redirectRefusingTransport{base: http.DefaultTransport},
		}
	}
	if _, ok := c.HTTP.Transport.(*redirectRefusingTransport); !ok {
		base := c.HTTP.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		c.HTTP.Transport = &redirectRefusingTransport{base: base}
	}
	return c.HTTP
}

// Do sends r with cred, decodes the JSON response into out (may be nil),
// and applies the retry-safety allowlist and backoff from
// docs/contract.md.
func (c *Client) Do(ctx context.Context, cred credentials.Credential, r Request, out any) error {
	if !Trusted(cred.Server) {
		return errForeignServer
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	retryable := Retryable(r.Method, r.Route, r.Key)
	attempts := r.Attempts
	if attempts <= 0 {
		attempts = defaultAttempts
	}
	if !retryable {
		attempts = 1
	}

	var body []byte
	if r.Body != nil {
		b, err := json.Marshal(r.Body)
		if err != nil {
			return fmt.Errorf("api: encoding request body: %w", err)
		}
		body = b
	}

	httpClient := c.httpClient(timeout)
	url := cred.Server + r.Route

	sleepFn := c.Sleep
	if sleepFn == nil {
		sleepFn = time.Sleep
	}

	var (
		lastErr *Error
		used    int
	)

	for attempt := 0; attempt < attempts; attempt++ {
		used = attempt + 1

		if ctxErr := ctx.Err(); ctxErr != nil {
			wrapped := fmt.Errorf("api: %w", ctxErr)
			c.observe(r, used, 0, NewError(0, "", nil), nil, cred)
			return wrapped
		}

		var reqBody io.Reader
		if body != nil {
			reqBody = bytes.NewReader(body)
		}
		httpReq, err := http.NewRequestWithContext(ctx, r.Method, url, reqBody)
		if err != nil {
			return fmt.Errorf("api: building request: %w", err)
		}
		setHeaders(httpReq, cred, c.ClientKind, r)

		resp, sendErr := httpClient.Do(httpReq)

		var (
			status     int
			apiErr     *Error
			decodedNow any
			noRetry    bool
		)

		switch {
		case sendErr != nil:
			var rr *redirectRefusedError
			switch {
			case errors.As(sendErr, &rr):
				status = rr.status
				apiErr = NewError(status, rr.header.Get("X-Request-ID"), parseRetryAfter(rr.header.Get("Retry-After"), time.Now()))
			case ctx.Err() != nil:
				wrapped := fmt.Errorf("api: %w", ctx.Err())
				c.observe(r, used, 0, NewError(0, "", nil), nil, cred)
				return wrapped
			default:
				status = 0
				apiErr = NewError(0, "", nil)
			}
		default:
			status = resp.StatusCode
			reqID := resp.Header.Get("X-Request-ID")
			if status >= 200 && status < 300 {
				if out != nil {
					if decErr := json.NewDecoder(resp.Body).Decode(out); decErr != nil {
						apiErr = NewError(502, reqID, nil)
						noRetry = true
					} else if m, ok := out.(*map[string]any); ok {
						decodedNow = *m
					}
				}
			} else {
				apiErr = NewError(status, reqID, parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()))
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}

		if apiErr == nil {
			c.observe(r, used, status, nil, decodedNow, cred)
			return nil
		}

		lastErr = apiErr

		transient := isTransientStatus(apiErr.Status)
		lastAttempt := attempt == attempts-1
		waitTooLong := apiErr.RetryAfter != nil && *apiErr.RetryAfter > maxRetryAfterWaitS
		if !retryable || !transient || lastAttempt || noRetry || waitTooLong {
			break
		}

		var wait time.Duration
		if apiErr.RetryAfter != nil {
			wait = time.Duration(*apiErr.RetryAfter) * time.Second
		} else {
			wait = backoff(attempt)
		}
		sleepFn(wait)
	}

	c.observe(r, used, lastErr.Status, lastErr, nil, cred)
	return lastErr
}

// setHeaders sets the Python-compatible headers plus the Go addition
// (User-Agent) on req.
func setHeaders(req *http.Request, cred credentials.Credential, clientKind string, r Request) {
	req.Header.Set("Content-Type", "application/json")

	kind := clientKind
	if kind == "" {
		kind = version.ClientKind
	}
	req.Header.Set("X-Ajent-Client", kind)
	req.Header.Set("X-Ajent-Client-Version", version.Version)
	req.Header.Set("User-Agent", version.UserAgent())

	if r.Method != http.MethodPost || r.Route != "/v1/signup" {
		req.Header.Set("Authorization", "Bearer "+cred.Token)
	}
	if r.Key != "" {
		req.Header.Set("Idempotency-Key", r.Key)
	}
}

// isTransientStatus reports whether status is one of the transient
// conditions (docs/contract.md section 7): connection failure (0), 429,
// 502, 503 or 504.
func isTransientStatus(status int) bool {
	switch status {
	case 0, 429, 502, 503, 504:
		return true
	}
	return false
}

// observe calls c.Observer.Observe unless the observer is nil or the
// request opted out.
func (c *Client) observe(r Request, attempts, status int, err *Error, result any, cred credentials.Credential) {
	if c.Observer == nil || r.NoObserve {
		return
	}
	c.Observer.Observe(Observation{
		Method:        r.Method,
		RouteTemplate: RouteTemplate(r.Route),
		Attempts:      attempts,
		Status:        status,
		Err:           err,
		Result:        result,
		Credential:    cred,
	})
}
