package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/fermentedsoybean7110/go-url-health-checker/internal/checker"
)

const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	timeout := fs.Duration("timeout", checker.DefaultTimeout, "timeout per request")
	concurrency := fs.Int("concurrency", checker.DefaultConcurrency, "maximum number of concurrent requests")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: healthcheck [-timeout 10s] [-concurrency 5] <url> [url...]")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}

	urls := fs.Args()
	switch {
	case len(urls) == 0:
		fmt.Fprintln(stderr, "error: at least one URL is required")
		fs.Usage()
		return exitUsage
	case *timeout < 0:
		fmt.Fprintln(stderr, "error: -timeout must not be negative")
		return exitUsage
	case *concurrency < 0:
		fmt.Fprintln(stderr, "error: -concurrency must not be negative")
		return exitUsage
	}

	results := checker.New(*timeout).CheckAll(ctx, urls, *concurrency)
	if printResults(stdout, results) {
		return exitOK
	}
	return exitFail
}

// printResults writes one line per result and reports whether all of them are OK (2xx).
func printResults(w io.Writer, results []checker.Result) bool {
	allOK := true
	for _, r := range results {
		label, code := "OK", fmt.Sprint(r.StatusCode)
		switch {
		case r.Err != nil:
			label, code = "ERROR", "-"
		case r.StatusCode < 200 || r.StatusCode > 299:
			label = "FAIL"
		}
		if label != "OK" {
			allOK = false
		}

		line := fmt.Sprintf("%-5s %3s %8s  %s", label, code, r.Duration.Round(time.Millisecond), r.URL)
		if r.Err != nil {
			line += fmt.Sprintf("  (%v)", r.Err)
		}
		fmt.Fprintln(w, line)
	}
	return allOK
}
