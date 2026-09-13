package whatsapp

import (
	"context"
	"time"

	"go.mau.fi/whatsmeow/appstate"
)

// An accepted_elsewhere notification ends companion ringing, before the primary
// device can publish a completed call log. Check again after 15, 60 and 180 seconds.
// One worker per provider coalesces requests; these checks never hold sync status open.
func (w *WhatsAppProvider) scheduleCallLogRefresh() {
	w.mu.RLock()
	client, ctx := w.client, w.ctx
	w.mu.RUnlock()
	if client == nil || ctx == nil || ctx.Err() != nil || w.getInstanceId() == "" {
		return
	}
	w.callLogRefreshMu.Lock()
	if w.callLogRefreshRunning {
		w.callLogRefreshPending = true
		w.callLogRefreshMu.Unlock()
		return
	}
	w.callLogRefreshRunning = true
	w.callLogRefreshMu.Unlock()
	w.log("WhatsApp: Scheduled post-call journal checks at 15s, 60s and 180s\n")
	go func() {
		for {
			completed := runCallLogRefreshCycle(ctx, []time.Duration{15 * time.Second, 45 * time.Second, 120 * time.Second}, func(fetchCtx context.Context) {
				before := w.callLogRecordsSeen.Load()
				for _, name := range []appstate.WAPatchName{appstate.WAPatchRegular, appstate.WAPatchRegularLow, appstate.WAPatchRegularHigh} {
					if err := client.FetchAppState(fetchCtx, name, false, false); err != nil {
						w.log("WhatsApp: Post-call journal check for %s failed: %v\n", name, err)
					}
				}
				w.log("WhatsApp: Post-call journal check completed: %d call log records observed during check\n", w.callLogRecordsSeen.Load()-before)
			})
			w.callLogRefreshMu.Lock()
			again := completed && w.callLogRefreshPending && ctx.Err() == nil
			w.callLogRefreshPending = false
			if !again {
				w.callLogRefreshRunning = false
			}
			w.callLogRefreshMu.Unlock()
			if !again {
				return
			}
		}
	}()
}

func runCallLogRefreshCycle(ctx context.Context, delays []time.Duration, fetch func(context.Context)) bool {
	for _, delay := range delays {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
		if ctx.Err() != nil {
			return false
		}
		fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		fetch(fetchCtx)
		cancel()
	}
	return ctx.Err() == nil
}
