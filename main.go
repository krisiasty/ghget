// Command ghget downloads and optionally extracts public GitHub release assets.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/krisiasty/ghget/internal/app"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:]))
}

func run(parent context.Context, args []string) int {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	httpClient := &http.Client{Timeout: 30 * time.Minute}
	if err := app.New(httpClient, os.Stdout, os.Stderr).Run(ctx, args); err != nil {
		fmt.Fprintf(os.Stderr, "ghget: %v\n", err)
		return 1
	}
	return 0
}
