package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type Runner struct {
	Cycle    func(context.Context) error
	Interval time.Duration
	Wait     func(context.Context, time.Duration) error
	Log      *slog.Logger
}

func (r Runner) Run(ctx context.Context) error {
	if r.Interval <= 0 || r.Cycle == nil {
		return errors.New("invalid worker runner configuration")
	}
	wait := r.Wait
	if wait == nil {
		wait = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	for ctx.Err() == nil {
		err := r.Cycle(ctx)
		if errors.Is(err, ErrLockLost) {
			return err
		}
		if err != nil && ctx.Err() == nil && r.Log != nil {
			r.Log.Error("worker cycle failed; retrying next interval", "error", err)
		}
		if ctx.Err() != nil {
			break
		}
		if err := wait(ctx, r.Interval); err != nil {
			if ctx.Err() == nil {
				return err
			}
			break
		}
	}
	if cause := context.Cause(ctx); errors.Is(cause, ErrLockLost) {
		return cause
	}
	return nil
}
