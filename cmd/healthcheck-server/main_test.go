package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fermentedsoybean7110/go-url-health-checker/internal/checker"
	"github.com/fermentedsoybean7110/go-url-health-checker/internal/server"
)

func TestRun_UsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"-unknown"}},
		{"unexpected argument", []string{"http://example.com"}},
		{"invalid timeout", []string{"-timeout", "abc"}},
		{"negative timeout", []string{"-timeout", "-1s"}},
		{"negative concurrency", []string{"-concurrency", "-1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer

			code := run(context.Background(), tt.args, &out, &errOut)

			if code != exitUsage {
				t.Errorf("exit code = %d, want %d", code, exitUsage)
			}
			if out.Len() != 0 {
				t.Errorf("stdout = %q, want empty", out.String())
			}
			if errOut.Len() == 0 {
				t.Error("stderr is empty, want error message")
			}
		})
	}
}

func TestRun_Help(t *testing.T) {
	var out, errOut bytes.Buffer

	code := run(context.Background(), []string{"-h"}, &out, &errOut)

	if code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.Contains(errOut.String(), "Usage:") {
		t.Errorf("stderr = %q, want usage", errOut.String())
	}
}

func TestRun_ListenError(t *testing.T) {
	var out, errOut bytes.Buffer

	code := run(context.Background(), []string{"-addr", "not-an-address"}, &out, &errOut)

	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if errOut.Len() == 0 {
		t.Error("stderr is empty, want error message")
	}
}

func TestRun_StartsAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	var out, errOut bytes.Buffer

	code := run(ctx, []string{"-addr", "127.0.0.1:0"}, &out, &errOut)

	if code != exitOK {
		t.Errorf("exit code = %d, want %d (stderr %q)", code, exitOK, errOut.String())
	}
	if !strings.Contains(out.String(), "listening on 127.0.0.1:") {
		t.Errorf("stdout = %q, want listening message", out.String())
	}
}

func TestServe_HandlesRequestsAndShutsDownGracefully(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := newHTTPServer(server.New(checker.New(time.Second), 1), time.Second, 1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, srv, ln) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || resp.StatusCode != http.StatusOK || body["status"] != "ok" {
		t.Errorf("status %d body %v (err %v), want 200 ok", resp.StatusCode, body, err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after cancel")
	}
}

func TestNewHTTPServer_Timeouts(t *testing.T) {
	srv := newHTTPServer(http.NewServeMux(), 10*time.Second, 5)

	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Errorf("read/idle timeouts must be set: %+v", srv)
	}
	// 50 URLs / 5 workers = 10 rounds x 10s, plus margin.
	if want := 105 * time.Second; srv.WriteTimeout != want {
		t.Errorf("WriteTimeout = %v, want %v", srv.WriteTimeout, want)
	}
}
