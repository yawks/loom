//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fblocks
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

extern void LoomWindowActivityChanged(int active);

static void StartLoomWindowActivityMonitor(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        void (^notify)(void) = ^{
            NSApplication *app = [NSApplication sharedApplication];
            NSWindow *window = app.keyWindow;
            LoomWindowActivityChanged(app.isActive && window != nil &&
                                      window.isVisible && !window.isMiniaturized);
        };
        NSNotificationCenter *center = [NSNotificationCenter defaultCenter];
        NSArray *names = @[NSApplicationDidBecomeActiveNotification,
                           NSApplicationDidResignActiveNotification,
                           NSApplicationDidHideNotification,
                           NSApplicationDidUnhideNotification,
                           NSWindowDidBecomeKeyNotification,
                           NSWindowDidResignKeyNotification,
                           NSWindowDidMiniaturizeNotification,
                           NSWindowDidDeminiaturizeNotification];
        for (NSString *name in names) {
            [center addObserverForName:name object:nil queue:[NSOperationQueue mainQueue]
                            usingBlock:^(NSNotification *note) { notify(); }];
        }
        notify();
    });
}
*/
import "C"

import "sync"

var windowActivityChannel = make(chan bool, 1)
var windowActivityOnce sync.Once

//export LoomWindowActivityChanged
func LoomWindowActivityChanged(active C.int) {
	// Notifications run serially on Cocoa's main queue. Keep the newest state
	// rather than losing an activation behind a buffered deactivation.
	select {
	case <-windowActivityChannel:
	default:
	}
	windowActivityChannel <- active != 0
}

func systemWindowActivityEvents() <-chan bool {
	windowActivityOnce.Do(func() { C.StartLoomWindowActivityMonitor() })
	return windowActivityChannel
}
