// Package checker provides URL health checking.
package checker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// DefaultTimeout is used when a non-positive timeout is given to New.
const DefaultTimeout = 10 * time.Second

// ErrInvalidURL is returned (wrapped) in Result.Err when the URL fails validation.
var ErrInvalidURL = errors.New("invalid url")

// Result is the outcome of a single URL check.
type Result struct {
	URL        string
	StatusCode int           // 0 when no response was received
	Duration   time.Duration // time until response headers were received
	Err        error
}

// Checker sends HTTP requests to check URL availability.
type Checker struct {
	client *http.Client
}

// New returns a Checker. Redirects are not followed; 3xx responses are reported as-is.
func New(timeout time.Duration) *Checker {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Checker{
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Check requests rawURL and reports the status code and response time.
// HTTP 4xx/5xx responses are not errors; failures are stored in Result.Err.
func (c *Checker) Check(ctx context.Context, rawURL string) Result {
	result := Result{URL: rawURL}

	if err := validateURL(rawURL); err != nil {
		result.Err = err
		return result
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		result.Err = fmt.Errorf("%w: %v", ErrInvalidURL, err)
		return result
	}

	start := time.Now()
	resp, err := c.client.Do(req)
	result.Duration = time.Since(start)
	if err != nil {
		result.Err = fmt.Errorf("request failed: %w", err)
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	return result
}

func validateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: unsupported scheme %q", ErrInvalidURL, u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("%w: missing host", ErrInvalidURL)
	}
	return nil
}
