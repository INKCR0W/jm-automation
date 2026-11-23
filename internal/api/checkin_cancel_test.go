package api

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestPerformCheckInCancelsRetryBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// A missing user ID causes a login-state error without sending an API request.
		checkIn := &CheckInAPI{}
		result := make(chan error, 1)
		go func() {
			result <- checkIn.PerformCheckIn(ctx)
		}()
		synctest.Wait()

		start := time.Now()
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Errorf("PerformCheckIn returned %v, want context.Canceled", err)
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Errorf("canceled retry waited %v", elapsed)
		}
	})
}
