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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	httpClient := &http.Client{Timeout: 30 * time.Minute}
	if err := app.New(httpClient, os.Stdout, os.Stderr).Run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "ghget: %v\n", err)
		os.Exit(1)
	}
}
