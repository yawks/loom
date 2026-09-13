package whatsapp

import (
	"context"
	"testing"
	"time"
)

func TestCallLogRefreshCycleBoundedAndCancelable(t *testing.T) {
	t.Run("three checks with deadlines", func(t *testing.T) {
		count := 0
		if !runCallLogRefreshCycle(context.Background(), []time.Duration{0, 0, 0}, func(ctx context.Context) {
			count++
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second {
				t.Fatal("missing bounded deadline")
			}
		}) || count != 3 {
			t.Fatalf("got %d checks", count)
		}
	})
	t.Run("disconnect cancels remaining checks", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		count := 0
		completed := runCallLogRefreshCycle(ctx, []time.Duration{0, time.Hour}, func(context.Context) { count++; cancel() })
		if completed || count != 1 {
			t.Fatalf("completed=%v checks=%d", completed, count)
		}
	})
	t.Run("already disconnected", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if runCallLogRefreshCycle(ctx, []time.Duration{0}, func(context.Context) { t.Fatal("unexpected fetch") }) {
			t.Fatal("unexpected completion")
		}
	})
}
