// Package checker provides URL health checking.
package checker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sync"
	"syscall"
	"time"
)

// DefaultTimeout is used when a non-positive timeout is given to New.
const DefaultTimeout = 10 * time.Second

// DefaultConcurrency is used when a non-positive concurrency is given to CheckAll.
const DefaultConcurrency = 5

// ErrInvalidURL is returned (wrapped) in Result.Err when the URL fails validation.
var ErrInvalidURL = errors.New("invalid url")

// ErrBlockedAddress is returned (wrapped) in Result.Err when the destination IP is not public
// and the Checker was created with WithBlockPrivateIPs.
var ErrBlockedAddress = errors.New("blocked address")

// nonPublicPrefixes lists ranges that netip.Addr's helper methods do not cover.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
}

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

// Option configures a Checker.
type Option func(*Checker)

// WithBlockPrivateIPs refuses connections to loopback, private, link-local, multicast and
// unspecified addresses (SSRF protection). The check runs on the resolved IP at dial time,
// so DNS rebinding cannot bypass it. Proxies are disabled because they would hide the real destination.
func WithBlockPrivateIPs() Option {
	return func(c *Checker) {
		dialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
			Control:   rejectNonPublicAddr,
		}
		c.client.Transport = &http.Transport{
			Proxy:               nil,
			DialContext:         dialer.DialContext,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		}
	}
}

// New returns a Checker. Redirects are not followed; 3xx responses are reported as-is.
func New(timeout time.Duration, opts ...Option) *Checker {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	c := &Checker{
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// rejectNonPublicAddr is a net.Dialer.Control hook; address is the already-resolved "ip:port".
func rejectNonPublicAddr(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBlockedAddress, err)
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBlockedAddress, err)
	}
	if isNonPublic(addr) {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, addr)
	}
	return nil
}

func isNonPublic(addr netip.Addr) bool {
	addr = addr.Unmap() // treat ::ffff:127.0.0.1 as 127.0.0.1
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsMulticast() || addr.IsUnspecified() {
		return true
	}
	for _, p := range nonPublicPrefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
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

// CheckAll checks urls with at most concurrency requests in flight.
// Results are returned in the same order as urls; duplicates are checked as-is.
func (c *Checker) CheckAll(ctx context.Context, urls []string, concurrency int) []Result {
	results := make([]Result, len(urls))
	if len(urls) == 0 {
		return results
	}

	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	workers := min(concurrency, len(urls))

	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each worker writes only to its own index, so no lock is needed.
			for i := range jobs {
				results[i] = c.Check(ctx, urls[i])
			}
		}()
	}

	for i := range urls {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return results
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
