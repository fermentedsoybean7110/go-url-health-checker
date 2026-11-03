package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fermentedsoybean7110/go-url-health-checker/internal/checker"
)

func newTarget(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, h http.Handler, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/check", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func jsonBody(t *testing.T, urls ...string) string {
	t.Helper()
	b, err := json.Marshal(checkRequest{URLs: urls})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) checkResponse {
	t.Helper()
	var resp checkResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v (body %q)", err, rec.Body.String())
	}
	return resp
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp errorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v (body %q)", err, rec.Body.String())
	}
	return resp.Error
}

func TestCheck_Success(t *testing.T) {
	ok := newTarget(t, http.StatusOK)
	notFound := newTarget(t, http.StatusNotFound)
	h := New(checker.New(time.Second), 2)

	rec := post(t, h, "application/json", jsonBody(t, ok.URL, notFound.URL, "ftp://example.com"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	resp := decodeResponse(t, rec)
	if len(resp.Results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(resp.Results))
	}
	if r := resp.Results[0]; r.URL != ok.URL || r.StatusCode != 200 || r.Error != "" {
		t.Errorf("results[0] = %+v, want 200 for %s", r, ok.URL)
	}
	if r := resp.Results[1]; r.URL != notFound.URL || r.StatusCode != 404 || r.Error != "" {
		t.Errorf("results[1] = %+v, want 404 for %s", r, notFound.URL)
	}
	if r := resp.Results[2]; r.StatusCode != 0 || !strings.Contains(r.Error, "invalid url") {
		t.Errorf("results[2] = %+v, want invalid url error", r)
	}
}

func TestCheck_ContentTypeWithCharset(t *testing.T) {
	ok := newTarget(t, http.StatusOK)
	h := New(checker.New(time.Second), 1)

	rec := post(t, h, "application/json; charset=utf-8", jsonBody(t, ok.URL))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestCheck_MaxURLsAccepted(t *testing.T) {
	ok := newTarget(t, http.StatusOK)
	urls := make([]string, MaxURLs)
	for i := range urls {
		urls[i] = ok.URL
	}
	h := New(checker.New(time.Second), 10)

	rec := post(t, h, "application/json", jsonBody(t, urls...))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if n := len(decodeResponse(t, rec).Results); n != MaxURLs {
		t.Errorf("len(results) = %d, want %d", n, MaxURLs)
	}
}

func TestCheck_BadRequests(t *testing.T) {
	tooMany := make([]string, MaxURLs+1)
	for i := range tooMany {
		tooMany[i] = "http://example.com/" + strconv.Itoa(i)
	}

	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
	}{
		{"missing content type", "", `{"urls":["http://example.com"]}`, http.StatusUnsupportedMediaType},
		{"wrong content type", "text/plain", `{"urls":["http://example.com"]}`, http.StatusUnsupportedMediaType},
		{"empty body", "application/json", ``, http.StatusBadRequest},
		{"malformed json", "application/json", `{"urls":`, http.StatusBadRequest},
		{"wrong type", "application/json", `{"urls":"http://example.com"}`, http.StatusBadRequest},
		{"unknown field", "application/json", `{"urls":["http://example.com"],"extra":1}`, http.StatusBadRequest},
		{"trailing data", "application/json", `{"urls":["http://example.com"]} {}`, http.StatusBadRequest},
		{"no urls field", "application/json", `{}`, http.StatusBadRequest},
		{"empty urls", "application/json", `{"urls":[]}`, http.StatusBadRequest},
		{"too many urls", "application/json", jsonBody(t, tooMany...), http.StatusBadRequest},
		{"body too large", "application/json", `{"urls":["` + strings.Repeat("a", maxBodyBytes) + `"]}`, http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := New(checker.New(time.Second), 1)

			rec := post(t, h, tt.contentType, tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if msg := decodeError(t, rec); msg == "" {
				t.Error("error message is empty")
			}
		})
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := New(checker.New(time.Second), 1)

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/check"},
		{http.MethodPut, "/check"},
		{http.MethodDelete, "/check"},
		{http.MethodPost, "/healthz"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))

			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want 405", rec.Code)
			}
		})
	}
}

func TestNotFound(t *testing.T) {
	h := New(checker.New(time.Second), 1)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/unknown", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHealthz(t *testing.T) {
	h := New(checker.New(time.Second), 1)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || body["status"] != "ok" {
		t.Errorf("body = %v (err %v), want status ok", body, err)
	}
}

func TestCheck_BlocksPrivateAddresses(t *testing.T) {
	loopback := newTarget(t, http.StatusOK)
	h := New(checker.New(2*time.Second, checker.WithBlockPrivateIPs()), 3)

	rec := post(t, h, "application/json",
		jsonBody(t, loopback.URL, "http://10.0.0.1/", "http://169.254.169.254/latest/meta-data/"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	for i, r := range decodeResponse(t, rec).Results {
		if r.StatusCode != 0 || !strings.Contains(r.Error, "blocked address") {
			t.Errorf("results[%d] = %+v, want blocked address error", i, r)
		}
	}
}
