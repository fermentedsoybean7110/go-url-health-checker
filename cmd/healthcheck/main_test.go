package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newStatusServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRun_AllOK(t *testing.T) {
	srv := newStatusServer(t, http.StatusOK)

	code, stdout, _ := runCLI(t, srv.URL)

	if code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.HasPrefix(stdout, "OK") || !strings.Contains(stdout, "200") || !strings.Contains(stdout, srv.URL) {
		t.Errorf("unexpected output: %q", stdout)
	}
}

func TestRun_FailureExitCode(t *testing.T) {
	ok := newStatusServer(t, http.StatusOK)
	notFound := newStatusServer(t, http.StatusNotFound)
	redirect := newStatusServer(t, http.StatusFound)
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()

	code, stdout, _ := runCLI(t, ok.URL, notFound.URL, redirect.URL, closedURL)

	if code != exitFail {
		t.Errorf("exit code = %d, want %d", code, exitFail)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4: %q", len(lines), stdout)
	}
	wantPrefixes := []string{"OK", "FAIL", "FAIL", "ERROR"}
	wantURLs := []string{ok.URL, notFound.URL, redirect.URL, closedURL}
	for i, line := range lines {
		if !strings.HasPrefix(line, wantPrefixes[i]) || !strings.Contains(line, wantURLs[i]) {
			t.Errorf("line %d = %q, want prefix %q and URL %q", i, line, wantPrefixes[i], wantURLs[i])
		}
	}
}

func TestRun_UsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no url", nil},
		{"unknown flag", []string{"-unknown", "http://example.com"}},
		{"invalid timeout", []string{"-timeout", "abc", "http://example.com"}},
		{"negative timeout", []string{"-timeout", "-1s", "http://example.com"}},
		{"negative concurrency", []string{"-concurrency", "-1", "http://example.com"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, tt.args...)

			if code != exitUsage {
				t.Errorf("exit code = %d, want %d", code, exitUsage)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if stderr == "" {
				t.Error("stderr is empty, want error message")
			}
		})
	}
}

func TestRun_Help(t *testing.T) {
	code, _, stderr := runCLI(t, "-h")

	if code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr = %q, want usage", stderr)
	}
}

func TestRun_TimeoutFlag(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	start := time.Now()
	code, stdout, _ := runCLI(t, "-timeout", "50ms", srv.URL)

	if code != exitFail {
		t.Errorf("exit code = %d, want %d", code, exitFail)
	}
	if !strings.HasPrefix(stdout, "ERROR") {
		t.Errorf("stdout = %q, want ERROR line", stdout)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %v, -timeout was not applied", elapsed)
	}
}

func TestRun_ConcurrencyFlag(t *testing.T) {
	srv := newStatusServer(t, http.StatusOK)

	code, stdout, _ := runCLI(t, "-concurrency", "1", srv.URL, srv.URL, srv.URL)

	if code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	if n := strings.Count(stdout, "OK"); n != 3 {
		t.Errorf("OK lines = %d, want 3: %q", n, stdout)
	}
}
