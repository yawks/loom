//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fblocks
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

extern void LoomSessionDidUnlock(void);

static void StartLoomSessionUnlockMonitor(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        NSNotificationCenter *workspaceCenter = [[NSWorkspace sharedWorkspace] notificationCenter];
        [workspaceCenter addObserverForName:NSWorkspaceSessionDidBecomeActiveNotification
                                     object:nil
                                      queue:[NSOperationQueue mainQueue]
                                 usingBlock:^(NSNotification *note) {
            LoomSessionDidUnlock();
        }];

        // The distributed notification covers the usual lock-screen unlock;
        // SessionDidBecomeActive additionally covers fast user switching.
        [[NSDistributedNotificationCenter defaultCenter]
            addObserverForName:@"com.apple.screenIsUnlocked"
                        object:nil
                         queue:[NSOperationQueue mainQueue]
                    usingBlock:^(NSNotification *note) {
            LoomSessionDidUnlock();
        }];
    });
}
*/
import "C"

var sessionUnlockChannel = make(chan struct{}, 1)

//export LoomSessionDidUnlock
func LoomSessionDidUnlock() {
	select {
	case sessionUnlockChannel <- struct{}{}:
	default:
	}
}

func systemSessionUnlockEvents() <-chan struct{} {
	C.StartLoomSessionUnlockMonitor()
	return sessionUnlockChannel
}
