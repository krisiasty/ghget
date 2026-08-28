// Command ghget downloads and optionally extracts public GitHub release assets.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/krisiasty/ghget/internal/app"
	"github.com/krisiasty/ghget/internal/httpclient"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:]))
}

func run(parent context.Context, args []string) int {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	httpClient := httpclient.New()
	if err := app.New(httpClient, os.Stdout, os.Stderr).Run(ctx, args); err != nil {
		fmt.Fprintf(os.Stderr, "ghget: %v\n", err)
		return 1
	}
	return 0
}
