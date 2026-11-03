# go-url-health-checker

[![CI](https://github.com/fermentedsoybean7110/go-url-health-checker/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/fermentedsoybean7110/go-url-health-checker/actions/workflows/ci.yml)

A URL health checker written in Go. It sends HTTP requests to the given URLs and reports the status code, response time, and errors such as timeouts and connection failures. It is available as a command-line tool and as a small REST API server.

This is a personal project, also published as a portfolio piece. It uses only the Go standard library and has no third-party dependencies.

## Features

- Reports HTTP status code and response time (time until response headers are received)
- Per-request timeout, always enforced
- Concurrent checks with a worker pool and a configurable concurrency limit; results keep the input order
- Redirects are not followed: a `3xx` response is reported as-is
- URL validation (only `http` and `https` with a host are accepted)
- SSRF protection for the API server (see [Security notes](#security-notes))
- Graceful shutdown of the API server

## Tech Stack

- Go (standard library only): `net/http`, `context`, goroutines and channels, `time`, `flag`, `encoding/json`
- Testing: `testing`, `net/http/httptest`, the race detector
- Docker (multi-stage build, distroless runtime image)
- GitHub Actions (gofmt, go vet, tests, Docker build)

## Architecture

```
.
├── cmd/
│   ├── healthcheck/         # CLI entry point
│   └── healthcheck-server/  # REST API server entry point
└── internal/
    ├── checker/             # URL checking logic (HTTP client, worker pool, SSRF protection)
    └── server/              # HTTP handlers for the REST API
```

- `internal/checker` has no knowledge of the CLI or the server. `Check` checks one URL, and `CheckAll` checks many with a bounded worker pool. Failures are returned in `Result.Err` instead of aborting the whole run.
- `cmd/*` packages keep `main()` thin: argument parsing and output live in a testable `run` function.
- `internal/server` is a plain `http.Handler`, so it can be tested with `httptest`.

## Setup

Requirements: Go 1.27.1 or later.

```bash
git clone https://github.com/fermentedsoybean7110/go-url-health-checker.git
cd go-url-health-checker
go build -o bin/healthcheck ./cmd/healthcheck
go build -o bin/healthcheck-server ./cmd/healthcheck-server
```

## Usage

### CLI

```
healthcheck [-timeout 10s] [-concurrency 5] <url> [url...]
```

| Flag           | Default | Description                              |
| -------------- | ------- | ---------------------------------------- |
| `-timeout`     | `10s`   | Timeout per request                      |
| `-concurrency` | `5`     | Maximum number of concurrent requests    |

Example (against local test servers; `/slow` takes 3 seconds to respond):

```console
$ healthcheck -timeout 1s -concurrency 2 \
    http://127.0.0.1:18090/ok http://127.0.0.1:18090/missing \
    http://127.0.0.1:18090/slow http://127.0.0.1:1/
OK    200      1ms  http://127.0.0.1:18090/ok
FAIL  404      1ms  http://127.0.0.1:18090/missing
ERROR   -   1.001s  http://127.0.0.1:18090/slow  (request failed: Get "http://127.0.0.1:18090/slow": context deadline exceeded (Client.Timeout exceeded while awaiting headers))
ERROR   -       0s  http://127.0.0.1:1/  (request failed: Get "http://127.0.0.1:1/": dial tcp 127.0.0.1:1: connect: connection refused)
```

One line is printed per URL, in input order:

| Label   | Meaning                                                    |
| ------- | ---------------------------------------------------------- |
| `OK`    | `2xx` response                                             |
| `FAIL`  | Any other status code, including `3xx` (redirects are not followed) |
| `ERROR` | Invalid URL, timeout, connection failure, etc.             |

Exit codes: `0` when every URL is `OK`, `1` when at least one is `FAIL` or `ERROR`, `2` on usage errors.

### REST API

```
healthcheck-server [-addr 127.0.0.1:8080] [-timeout 10s] [-concurrency 5]
```

| Endpoint        | Description                                                  |
| --------------- | ------------------------------------------------------------ |
| `POST /check`   | Check up to 50 URLs. Body: `{"urls": ["..."]}` (`Content-Type: application/json`) |
| `GET /healthz`  | Liveness check of the server itself                          |

```console
$ curl -s -X POST -H 'Content-Type: application/json' \
    -d '{"urls":["https://example.com","http://169.254.169.254/","ftp://example.com"]}' \
    http://127.0.0.1:8080/check
{"results":[{"url":"https://example.com","status_code":200,"duration_ms":317},{"url":"http://169.254.169.254/","status_code":0,"duration_ms":0,"error":"request failed: Get \"http://169.254.169.254/\": dial tcp 169.254.169.254:80: blocked address: 169.254.169.254"},{"url":"ftp://example.com","status_code":0,"duration_ms":0,"error":"invalid url: unsupported scheme \"ftp\""}]}
```

Failures of individual URLs (invalid URL, blocked address, timeout, ...) are reported in each result's `error` field with HTTP status `200`. Request-level problems return a JSON error:

| Status | Cause                                                                 |
| ------ | --------------------------------------------------------------------- |
| `400`  | Malformed JSON, unknown fields, empty `urls`, or more than 50 URLs    |
| `405`  | Unsupported method                                                    |
| `413`  | Request body larger than 128 KiB                                      |
| `415`  | `Content-Type` is not `application/json`                              |

```console
$ curl -s -X POST -H 'Content-Type: application/json' -d '{"urls":[]}' http://127.0.0.1:8080/check
{"error":"urls must not be empty"}
```

### Docker

The image runs the API server as a non-root user.

```bash
docker build -t go-url-health-checker .
docker run --rm -p 127.0.0.1:8080:8080 go-url-health-checker
```

Publish the port on `127.0.0.1` as shown above: the server has no authentication.

## Security notes

- The API server blocks requests to non-public addresses: loopback, private ranges, link-local (including cloud metadata endpoints such as `169.254.169.254`), multicast, unspecified, `0.0.0.0/8`, `100.64.0.0/10`, and IPv4-mapped IPv6 forms of these. The check runs on the resolved IP at dial time, so DNS rebinding cannot bypass it. Proxies are disabled for this reason.
- The CLI does not apply this restriction, so it can check `localhost` and internal hosts.
- Consequently, the API server cannot check internal hosts.
- Limitations: no authentication (it listens on `127.0.0.1` by default), and IPv6 transition addresses such as NAT64 and 6to4 are not covered.
- Request bodies are limited to 128 KiB and 50 URLs, and the HTTP server sets read, write, and idle timeouts.

## Testing

```bash
go test -race -cover ./...
go vet ./...
gofmt -l .   # prints nothing when everything is formatted
```

Tests use `httptest` and do not depend on external websites.

## License

[MIT](LICENSE)
