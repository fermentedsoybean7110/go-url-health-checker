package checker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsNonPublic(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.255.255.255", true},
		{"::1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.1.1", true},
		{"169.254.169.254", true},
		{"0.0.0.0", true},
		{"0.1.2.3", true},
		{"::", true},
		{"100.64.0.1", true},
		{"224.0.0.1", true},
		{"ff02::1", true},
		{"fc00::1", true},
		{"fe80::1", true},
		{"::ffff:127.0.0.1", true},
		{"::ffff:10.0.0.1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"172.32.0.1", false},
		{"100.128.0.1", false},
		{"2001:4860:4860::8888", false},
		{"::ffff:8.8.8.8", false},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := isNonPublic(netip.MustParseAddr(tt.addr)); got != tt.want {
				t.Errorf("isNonPublic(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestCheck_BlockPrivateIPs(t *testing.T) {
	var hit atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hit.Store(true)
	}))
	defer srv.Close()

	tests := []struct {
		name string
		url  string
	}{
		{"loopback httptest server", srv.URL},
		{"localhost", "http://localhost:1/"},
		{"ipv6 loopback", "http://[::1]:1/"},
		{"private 10/8", "http://10.0.0.1/"},
		{"private 192.168/16", "http://192.168.0.1/"},
		{"cloud metadata", "http://169.254.169.254/latest/meta-data/"},
		{"unspecified", "http://0.0.0.0:1/"},
		{"ipv4-mapped ipv6 loopback", "http://[::ffff:127.0.0.1]:1/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New(2*time.Second, WithBlockPrivateIPs()).Check(context.Background(), tt.url)

			if !errors.Is(got.Err, ErrBlockedAddress) {
				t.Errorf("error = %v, want ErrBlockedAddress", got.Err)
			}
			if got.StatusCode != 0 {
				t.Errorf("StatusCode = %d, want 0", got.StatusCode)
			}
		})
	}

	if hit.Load() {
		t.Error("blocked request reached the server")
	}
}

func TestCheck_BlockPrivateIPs_DoesNotAffectDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	got := New(time.Second).Check(context.Background(), srv.URL)

	if got.Err != nil || got.StatusCode != http.StatusOK {
		t.Errorf("result = %+v, want 200 without error", got)
	}
}

func TestCheckAll_BlockPrivateIPs(t *testing.T) {
	urls := []string{"http://10.0.0.1/", "http://127.0.0.1:1/", "http://192.168.0.1/"}

	got := New(2*time.Second, WithBlockPrivateIPs()).CheckAll(context.Background(), urls, 2)

	for i, r := range got {
		if !errors.Is(r.Err, ErrBlockedAddress) {
			t.Errorf("results[%d].Err = %v, want ErrBlockedAddress", i, r.Err)
		}
	}
}
