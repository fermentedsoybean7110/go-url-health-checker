package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/fermentedsoybean7110/go-url-health-checker/internal/checker"
	"github.com/fermentedsoybean7110/go-url-health-checker/internal/server"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2

	defaultAddr         = "127.0.0.1:8080"
	shutdownGracePeriod = 10 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("healthcheck-server", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", defaultAddr, "listen address (no authentication: do not expose to untrusted networks)")
	timeout := fs.Duration("timeout", checker.DefaultTimeout, "timeout per checked URL")
	concurrency := fs.Int("concurrency", checker.DefaultConcurrency, "maximum number of concurrent checks per request")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: healthcheck-server [-addr 127.0.0.1:8080] [-timeout 10s] [-concurrency 5]")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}

	switch {
	case fs.NArg() > 0:
		fmt.Fprintln(stderr, "error: unexpected arguments:", fs.Args())
		fs.Usage()
		return exitUsage
	case *timeout < 0:
		fmt.Fprintln(stderr, "error: -timeout must not be negative")
		return exitUsage
	case *concurrency < 0:
		fmt.Fprintln(stderr, "error: -concurrency must not be negative")
		return exitUsage
	}

	if *timeout == 0 {
		*timeout = checker.DefaultTimeout
	}
	if *concurrency == 0 {
		*concurrency = checker.DefaultConcurrency
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitError
	}

	c := checker.New(*timeout, checker.WithBlockPrivateIPs())
	srv := newHTTPServer(server.New(c, *concurrency), *timeout, *concurrency)

	fmt.Fprintln(stdout, "listening on", ln.Addr())
	if err := serve(ctx, srv, ln); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return exitError
	}
	return exitOK
}

func newHTTPServer(h http.Handler, timeout time.Duration, concurrency int) *http.Server {
	// A full request runs ceil(MaxURLs/concurrency) rounds of up to timeout each.
	rounds := (server.MaxURLs + concurrency - 1) / concurrency
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      time.Duration(rounds)*timeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// serve runs srv on ln until ctx is canceled, then shuts it down gracefully.
func serve(ctx context.Context, srv *http.Server, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGracePeriod)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
