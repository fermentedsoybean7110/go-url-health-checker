// Package server exposes the URL checker over HTTP.
package server

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/fermentedsoybean7110/go-url-health-checker/internal/checker"
)

const (
	// MaxURLs is the maximum number of URLs accepted in one request.
	MaxURLs = 50

	maxBodyBytes = 128 << 10
)

type checkRequest struct {
	URLs []string `json:"urls"`
}

type checkResponse struct {
	Results []resultJSON `json:"results"`
}

type resultJSON struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type handler struct {
	checker     *checker.Checker
	concurrency int
}

// New returns an http.Handler serving POST /check and GET /healthz.
// concurrency is the maximum number of simultaneous checks per request.
func New(c *checker.Checker, concurrency int) http.Handler {
	h := &handler{checker: c, concurrency: concurrency}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /check", h.check)
	mux.HandleFunc("GET /healthz", h.healthz)
	return mux
}

func (h *handler) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handler) check(w http.ResponseWriter, r *http.Request) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return
	}

	req, status, err := decodeRequest(w, r)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}

	results := h.checker.CheckAll(r.Context(), req.URLs, h.concurrency)

	resp := checkResponse{Results: make([]resultJSON, len(results))}
	for i, res := range results {
		resp.Results[i] = resultJSON{
			URL:        res.URL,
			StatusCode: res.StatusCode,
			DurationMS: res.Duration.Milliseconds(),
		}
		if res.Err != nil {
			resp.Results[i].Error = res.Err.Error()
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func decodeRequest(w http.ResponseWriter, r *http.Request) (checkRequest, int, error) {
	var req checkRequest

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return req, http.StatusRequestEntityTooLarge, errors.New("request body too large")
		}
		return req, http.StatusBadRequest, errors.New("invalid JSON body")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return req, http.StatusBadRequest, errors.New("request body must contain a single JSON object")
	}

	switch {
	case len(req.URLs) == 0:
		return req, http.StatusBadRequest, errors.New("urls must not be empty")
	case len(req.URLs) > MaxURLs:
		return req, http.StatusBadRequest, errors.New("too many urls")
	}
	return req, http.StatusOK, nil
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
