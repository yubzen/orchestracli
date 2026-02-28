package agent

import (
	"context"
	"errors"
)

// ErrUserCancelled is returned when a run is cancelled via context cancellation.
var ErrUserCancelled = errors.New("user cancelled run")

func normalizeCancellationErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrUserCancelled) || errors.Is(err, context.Canceled) {
		return ErrUserCancelled
	}
	return err
}

func checkContextCancelled(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return normalizeCancellationErr(err)
	}
	return nil
}

func IsUserCancelled(err error) bool {
	return errors.Is(normalizeCancellationErr(err), ErrUserCancelled)
}
