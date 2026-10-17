package main

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestSignalContextCancelsOnInterrupt(t *testing.T) {
	ctx, stop := newSignalContext(context.Background())
	defer stop()

	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Signal(os.Interrupt); err != nil {
		t.Fatalf("send interrupt: %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("signal context was not cancelled by interrupt")
	}
}
