//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

void SetWindowCornerRadius(double radius) {
    dispatch_async(dispatch_get_main_queue(), ^{
        NSWindow *window = [[NSApplication sharedApplication] keyWindow];
        if (window == nil) {
            return;
        }
        // Try the private KVC key first (works on macOS ≤15).
        // On macOS 16+ it was removed; fall back to rounding the NSThemeFrame
        // (contentView's parent), which gives the same visual result.
        @try {
            [window setValue:@(radius) forKey:@"_cornerRadius"];
        } @catch (NSException *) {
            NSView *themeFrame = [[window contentView] superview];
            if (themeFrame != nil) {
                themeFrame.wantsLayer = YES;
                themeFrame.layer.cornerRadius = radius;
                themeFrame.layer.masksToBounds = YES;
            }
        }
    });
}
*/
import "C"

func setWindowCornerRadius(radius float64) {
	C.SetWindowCornerRadius(C.double(radius))
}
