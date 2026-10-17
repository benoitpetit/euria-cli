//go:build windows

package main

import (
	"context"
	"testing"
	"time"
)

func TestSignalContextCanBeStopped(t *testing.T) {
	ctx, stop := newSignalContext(context.Background())
	stop()

	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("stopping signal context did not cancel it")
	}
}
