package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

void SetDockBadge(const char *label) {
    NSString *badge = [NSString stringWithUTF8String:label];
    [[NSApp dockTile] setBadgeLabel:badge];
}
*/
import "C"
import "unsafe"

// setCgoDockBadge sets the dock badge label using native CGO call.
// Pass an empty string to clear the badge.
func setCgoDockBadge(label string) {
	cLabel := C.CString(label)
	defer C.free(unsafe.Pointer(cLabel))
	C.SetDockBadge(cLabel)
}
