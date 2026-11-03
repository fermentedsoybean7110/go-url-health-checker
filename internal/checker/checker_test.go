package checker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheck_StatusCodes(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{"ok", http.StatusOK},
		{"no content", http.StatusNoContent},
		{"moved permanently", http.StatusMovedPermanently},
		{"not found", http.StatusNotFound},
		{"server error", http.StatusInternalServerError},
		{"service unavailable", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			got := New(time.Second).Check(context.Background(), srv.URL)

			if got.Err != nil {
				t.Fatalf("unexpected error: %v", got.Err)
			}
			if got.StatusCode != tt.status {
				t.Errorf("StatusCode = %d, want %d", got.StatusCode, tt.status)
			}
			if got.Duration <= 0 {
				t.Errorf("Duration = %v, want > 0", got.Duration)
			}
			if got.URL != srv.URL {
				t.Errorf("URL = %q, want %q", got.URL, srv.URL)
			}
		})
	}
}

func TestCheck_DoesNotFollowRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/target" {
			t.Error("redirect target must not be requested")
		}
		http.Redirect(w, r, "/target", http.StatusFound)
	}))
	defer srv.Close()

	got := New(time.Second).Check(context.Background(), srv.URL)

	if got.Err != nil {
		t.Fatalf("unexpected error: %v", got.Err)
	}
	if got.StatusCode != http.StatusFound {
		t.Errorf("StatusCode = %d, want %d", got.StatusCode, http.StatusFound)
	}
}

func TestCheck_Timeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	got := New(50*time.Millisecond).Check(context.Background(), srv.URL)

	if got.Err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	var netErr interface{ Timeout() bool }
	if !errors.As(got.Err, &netErr) || !netErr.Timeout() {
		t.Errorf("error = %v, want timeout error", got.Err)
	}
	if got.StatusCode != 0 {
		t.Errorf("StatusCode = %d, want 0", got.StatusCode)
	}
}

func TestCheck_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := New(time.Second).Check(ctx, srv.URL)

	if !errors.Is(got.Err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", got.Err)
	}
}

func TestCheck_InvalidURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"no scheme", "example.com"},
		{"unsupported scheme", "ftp://example.com"},
		{"missing host", "http://"},
		{"malformed", "http://exa mple.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New(time.Second).Check(context.Background(), tt.url)

			if !errors.Is(got.Err, ErrInvalidURL) {
				t.Errorf("error = %v, want ErrInvalidURL", got.Err)
			}
			if got.StatusCode != 0 {
				t.Errorf("StatusCode = %d, want 0", got.StatusCode)
			}
		})
	}
}

func TestCheck_ConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	got := New(time.Second).Check(context.Background(), url)

	if got.Err == nil {
		t.Fatal("expected connection error, got nil")
	}
	if errors.Is(got.Err, ErrInvalidURL) {
		t.Errorf("error = %v, should not be ErrInvalidURL", got.Err)
	}
	if got.StatusCode != 0 {
		t.Errorf("StatusCode = %d, want 0", got.StatusCode)
	}
}

func TestCheckAll_PreservesOrder(t *testing.T) {
	statuses := []int{http.StatusOK, http.StatusNotFound, http.StatusInternalServerError, http.StatusAccepted}
	delays := []time.Duration{60 * time.Millisecond, 0, 30 * time.Millisecond, 10 * time.Millisecond}

	urls := make([]string, len(statuses))
	for i := range statuses {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(delays[i])
			w.WriteHeader(statuses[i])
		}))
		defer srv.Close()
		urls[i] = srv.URL
	}

	got := New(time.Second).CheckAll(context.Background(), urls, len(urls))

	if len(got) != len(urls) {
		t.Fatalf("len(results) = %d, want %d", len(got), len(urls))
	}
	for i, r := range got {
		if r.URL != urls[i] || r.StatusCode != statuses[i] || r.Err != nil {
			t.Errorf("results[%d] = %+v, want URL %q status %d", i, r, urls[i], statuses[i])
		}
	}
}

func TestCheckAll_MixedResults(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer ok.Close()
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()

	urls := []string{ok.URL, "not a url", closedURL, ok.URL}
	got := New(time.Second).CheckAll(context.Background(), urls, 2)

	if len(got) != len(urls) {
		t.Fatalf("len(results) = %d, want %d", len(got), len(urls))
	}
	if got[0].Err != nil || got[0].StatusCode != http.StatusOK {
		t.Errorf("results[0] = %+v, want 200", got[0])
	}
	if !errors.Is(got[1].Err, ErrInvalidURL) {
		t.Errorf("results[1].Err = %v, want ErrInvalidURL", got[1].Err)
	}
	if got[2].Err == nil || errors.Is(got[2].Err, ErrInvalidURL) {
		t.Errorf("results[2].Err = %v, want connection error", got[2].Err)
	}
	if got[3].Err != nil || got[3].StatusCode != http.StatusOK {
		t.Errorf("results[3] = %+v, want 200 (duplicate URL)", got[3])
	}
}

func TestCheckAll_ConcurrencyLimit(t *testing.T) {
	const limit, total = 3, 9

	var inFlight, maxInFlight atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		n := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
	}))
	defer srv.Close()

	urls := make([]string, total)
	for i := range urls {
		urls[i] = srv.URL
	}
	got := New(time.Second).CheckAll(context.Background(), urls, limit)

	for i, r := range got {
		if r.Err != nil {
			t.Errorf("results[%d].Err = %v", i, r.Err)
		}
	}
	if m := maxInFlight.Load(); m > limit {
		t.Errorf("max in-flight = %d, want <= %d", m, limit)
	}
}

func TestCheckAll_RunsConcurrently(t *testing.T) {
	const total = 4

	// Each handler waits until all requests have arrived, so this deadlocks (and times out) if run serially.
	var arrived sync.WaitGroup
	arrived.Add(total)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		arrived.Done()
		arrived.Wait()
	}))
	defer srv.Close()

	urls := make([]string, total)
	for i := range urls {
		urls[i] = srv.URL
	}
	got := New(2*time.Second).CheckAll(context.Background(), urls, total)

	for i, r := range got {
		if r.Err != nil {
			t.Errorf("results[%d].Err = %v", i, r.Err)
		}
	}
}

func TestCheckAll_Empty(t *testing.T) {
	got := New(time.Second).CheckAll(context.Background(), nil, 3)

	if len(got) != 0 {
		t.Errorf("len(results) = %d, want 0", len(got))
	}
}

func TestCheckAll_DefaultConcurrency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	got := New(time.Second).CheckAll(context.Background(), []string{srv.URL, srv.URL}, 0)

	if len(got) != 2 || got[0].Err != nil || got[1].Err != nil {
		t.Errorf("results = %+v, want 2 successful results", got)
	}
}

func TestCheckAll_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	urls := []string{srv.URL, srv.URL, srv.URL}
	got := New(time.Second).CheckAll(ctx, urls, 2)

	if len(got) != len(urls) {
		t.Fatalf("len(results) = %d, want %d", len(got), len(urls))
	}
	for i, r := range got {
		if !errors.Is(r.Err, context.Canceled) {
			t.Errorf("results[%d].Err = %v, want context.Canceled", i, r.Err)
		}
	}
}

func TestCheck_ContextDeadlineExceeded(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// The client timeout is longer, so the context deadline must be what fires.
	got := New(5*time.Second).Check(ctx, srv.URL)

	if !errors.Is(got.Err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", got.Err)
	}
}

func TestCheck_LargeBodyIsNotRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 64*1024)
		for range 256 { // 16 MiB
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	got := New(2*time.Second).Check(context.Background(), srv.URL)

	if got.Err != nil || got.StatusCode != http.StatusOK {
		t.Errorf("result = %+v, want 200 without error", got)
	}
}

func TestCheck_HTTPS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Run("untrusted certificate is an error", func(t *testing.T) {
		got := New(time.Second).Check(context.Background(), srv.URL)

		if got.Err == nil {
			t.Fatal("expected certificate error, got nil")
		}
		if got.StatusCode != 0 {
			t.Errorf("StatusCode = %d, want 0", got.StatusCode)
		}
	})

	t.Run("trusted certificate succeeds", func(t *testing.T) {
		c := New(time.Second)
		c.client.Transport = srv.Client().Transport

		got := c.Check(context.Background(), srv.URL)

		if got.Err != nil || got.StatusCode != http.StatusOK {
			t.Errorf("result = %+v, want 200 without error", got)
		}
	})
}

func TestCheckAll_ConcurrencyExceedsURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	urls := []string{srv.URL, srv.URL}
	got := New(time.Second).CheckAll(context.Background(), urls, 100)

	if len(got) != len(urls) {
		t.Fatalf("len(results) = %d, want %d", len(got), len(urls))
	}
	for i, r := range got {
		if r.Err != nil {
			t.Errorf("results[%d].Err = %v", i, r.Err)
		}
	}
}

func TestCheckAll_ManyURLsKeepOrder(t *testing.T) {
	const total = 100

	// The path index selects the status code, so each result can be matched to its input.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(200 + n)
	}))
	defer srv.Close()

	urls := make([]string, total)
	for i := range urls {
		urls[i] = srv.URL + "/" + strconv.Itoa(i)
	}
	got := New(5*time.Second).CheckAll(context.Background(), urls, 8)

	if len(got) != total {
		t.Fatalf("len(results) = %d, want %d", len(got), total)
	}
	for i, r := range got {
		if r.Err != nil || r.URL != urls[i] || r.StatusCode != 200+i {
			t.Errorf("results[%d] = %+v, want URL %q status %d", i, r, urls[i], 200+i)
		}
	}
}

func TestCheckAll_CanceledMidway(t *testing.T) {
	fast := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer fast.Close()

	arrived := make(chan struct{}, 1)
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-r.Context().Done()
	}))
	defer slow.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-arrived
		cancel()
	}()

	// With a single worker, the first URL completes, the second is in flight when canceled, the rest never start.
	urls := []string{fast.URL, slow.URL, fast.URL, fast.URL}
	got := New(5*time.Second).CheckAll(ctx, urls, 1)

	if len(got) != len(urls) {
		t.Fatalf("len(results) = %d, want %d", len(got), len(urls))
	}
	if got[0].Err != nil || got[0].StatusCode != http.StatusOK {
		t.Errorf("results[0] = %+v, want 200 without error", got[0])
	}
	for i := 1; i < len(got); i++ {
		if !errors.Is(got[i].Err, context.Canceled) {
			t.Errorf("results[%d].Err = %v, want context.Canceled", i, got[i].Err)
		}
	}
}

func TestNew_DefaultTimeout(t *testing.T) {
	if got := New(0).client.Timeout; got != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", got, DefaultTimeout)
	}
}
