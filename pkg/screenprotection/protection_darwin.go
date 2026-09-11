//go:build darwin

package screenprotection

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static int loomSetCaptureProtection(int enabled) {
    __block int found = 0;
    void (^apply)(void) = ^{
        // Do not use keyWindow: a dialog or DevTools may currently have focus.
        Class windowClass = NSClassFromString(@"WailsWindow");
        for (NSWindow *window in NSApp.windows) {
            if (windowClass && [window isKindOfClass:windowClass]) {
                window.sharingType = enabled ? NSWindowSharingNone : NSWindowSharingReadOnly;
                found = window.sharingType == (enabled ? NSWindowSharingNone : NSWindowSharingReadOnly);
                break;
            }
        }
    };
    if ([NSThread isMainThread]) apply();
    else dispatch_sync(dispatch_get_main_queue(), apply);
    return found;
}
*/
import "C"

import "fmt"

// ScreenCaptureKit can ignore NSWindowSharingNone. Never advertise this as
// guaranteed exclusion, even when setting the window property succeeds.
func Supported() bool { return true }
func Limited() bool   { return true }

func set(enabled bool) error {
	value := C.int(0)
	if enabled {
		value = 1
	}
	if C.loomSetCaptureProtection(value) == 0 {
		return fmt.Errorf("main window capture protection could not be applied")
	}
	return nil
}
