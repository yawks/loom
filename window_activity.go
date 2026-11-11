package main

import (
	"context"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Native application notifications are important for WKWebView: document focus
// and visibility alone can remain true after switching to another application.
// This suspends only frontend read polling, never provider streams or sync.
func (a *App) watchWindowActivity(ctx context.Context) {
	events := systemWindowActivityEvents()
	var previous bool
	var initialized bool
	for {
		select {
		case <-ctx.Done():
			return
		case active := <-events:
			if !initialized || active != previous {
				initialized, previous = true, active
				runtime.EventsEmit(ctx, "window-activity", active)
			}
		}
	}
}
