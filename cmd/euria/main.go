package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/benoitpetit/euria-cli/internal/cli"
)

func main() {
	ctx, stop := newSignalContext(context.Background())
	err := cli.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, cli.Dependencies{})
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "euria:", err)
		os.Exit(1)
	}
}

func newSignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}
