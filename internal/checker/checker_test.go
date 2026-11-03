package checker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheck_StatusCodes(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{"ok", http.StatusOK},
		{"not found", http.StatusNotFound},
		{"server error", http.StatusInternalServerError},
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

func TestNew_DefaultTimeout(t *testing.T) {
	if got := New(0).client.Timeout; got != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", got, DefaultTimeout)
	}
}
