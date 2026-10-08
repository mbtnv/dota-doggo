package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunnerWaitsAfterCompletionAndRecovers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cycles, waits := 0, 0
	err := (Runner{Interval: time.Minute, Cycle: func(context.Context) error {
		cycles++
		if cycles == 3 {
			cancel()
		}
		return errors.New("temporary failure")
	}, Wait: func(ctx context.Context, d time.Duration) error {
		waits++
		if cycles != waits || d != time.Minute {
			t.Fatal(cycles, waits, d)
		}
		return ctx.Err()
	}}).Run(ctx)
	if err != nil || cycles != 3 || waits != 2 {
		t.Fatal(err, cycles, waits)
	}
}
func TestRunnerStopsOnLockLossAndCancellation(t *testing.T) {
	calls := 0
	err := (Runner{Interval: time.Minute, Cycle: func(context.Context) error { calls++; return ErrLockLost }}).Run(context.Background())
	if !errors.Is(err, ErrLockLost) || calls != 1 {
		t.Fatal(err, calls)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(ErrLockLost)
	err = (Runner{Interval: time.Minute, Cycle: func(context.Context) error { t.Fatal("cycle after cancellation"); return nil }}).Run(ctx)
	if !errors.Is(err, ErrLockLost) {
		t.Fatal(err)
	}
}
