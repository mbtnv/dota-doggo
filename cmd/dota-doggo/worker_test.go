package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"dota-doggo/internal/jobs"
)

func TestWorkerMonitorCancelsOnLockLoss(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	done := make(chan struct{})
	go func() {
		monitorWorker(ctx, cancel, func(context.Context) error { return jobs.ErrLockLost }, time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("monitor did not stop")
	}
	if !errors.Is(context.Cause(ctx), jobs.ErrLockLost) {
		t.Fatal(context.Cause(ctx))
	}
}
func TestWorkerMonitorDoesNotPingAfterShutdown(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(nil)
	monitorWorker(ctx, cancel, func(context.Context) error { t.Fatal("ping after shutdown"); return nil }, time.Millisecond)
}
