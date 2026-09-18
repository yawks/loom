//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fblocks
#cgo LDFLAGS: -framework Carbon -framework Foundation -framework Speech
#include <stdlib.h>
#import <Carbon/Carbon.h>
#import <Foundation/Foundation.h>
#import <Speech/Speech.h>

static char *LoomSpeechError(NSString *message) {
    return strdup(message.UTF8String ?: "Speech recognition failed");
}

static BOOL LoomHasSpeechUsageDescription(void) {
    NSString *description = [NSBundle.mainBundle objectForInfoDictionaryKey:@"NSSpeechRecognitionUsageDescription"];
    return description.length > 0;
}

static NSString *LoomGetKeyboardLanguage(void) {
    __block NSString *language = nil;
    void (^readKeyboardLanguage)(void) = ^{
        TISInputSourceRef source = TISCopyCurrentKeyboardInputSource();
        NSArray *languages = source ? (NSArray *)TISGetInputSourceProperty(source, kTISPropertyInputSourceLanguages) : nil;
        language = [[languages firstObject] copy];
        if (source) CFRelease(source);
    };
    if ([NSThread isMainThread]) readKeyboardLanguage();
    else dispatch_sync(dispatch_get_main_queue(), readKeyboardLanguage);
    return [language autorelease];
}

static NSLocale *LoomResolveSpeechLocale(const char *requestedIdentifier) {
    if (requestedIdentifier && strlen(requestedIdentifier) > 0) {
        NSString *identifier = [NSString stringWithUTF8String:requestedIdentifier];
        NSLocale *locale = [NSLocale localeWithLocaleIdentifier:identifier];
        if (locale) return locale;
    }

    NSString *kbdLang = LoomGetKeyboardLanguage();
    NSLocale *userLocale = NSLocale.currentLocale;

    NSDictionary *dictationPrefs = [NSUserDefaults.standardUserDefaults persistentDomainForName:@"com.apple.speech.recognition.AppleSpeechRecognition.prefs"];
    NSDictionary *visibleSR = dictationPrefs[@"VisibleNetworkSRLocaleIdentifiers"];

    NSLocale *bestMatch = nil;
    NSLocale *onDeviceMatch = nil;
    NSLocale *anyMatch = nil;

    for (NSLocale *loc in SFSpeechRecognizer.supportedLocales) {
        SFSpeechRecognizer *rec = [[SFSpeechRecognizer alloc] initWithLocale:loc];
        BOOL onDevice = rec.supportsOnDeviceRecognition;
        NSString *locId = loc.localeIdentifier;
        NSString *locIdUnderscore = [locId stringByReplacingOccurrencesOfString:@"-" withString:@"_"];
        BOOL isDictation = (visibleSR[locId] != nil || visibleSR[locIdUnderscore] != nil);
        [rec release];

        BOOL matchesKbd = kbdLang.length && [locId hasPrefix:kbdLang];
        BOOL matchesUserLang = userLocale.languageCode.length && [locId hasPrefix:userLocale.languageCode];

        if (matchesKbd && isDictation && onDevice) {
            return loc;
        }
        if (matchesKbd && onDevice && !onDeviceMatch) {
            onDeviceMatch = loc;
        }
        if (matchesUserLang && isDictation && onDevice && !bestMatch) {
            bestMatch = loc;
        }
        if ((matchesKbd || matchesUserLang) && !anyMatch) {
            anyMatch = loc;
        }
    }

    if (onDeviceMatch) return onDeviceMatch;
    if (bestMatch) return bestMatch;
    if (anyMatch) return anyMatch;
    return NSLocale.currentLocale;
}

static char *LoomGetSpeechLocalesJSON(void) {
    @autoreleasepool {
        NSLocale *userLocale = NSLocale.currentLocale;
        NSString *kbdLang = LoomGetKeyboardLanguage();

        NSDictionary *dictationPrefs = [NSUserDefaults.standardUserDefaults persistentDomainForName:@"com.apple.speech.recognition.AppleSpeechRecognition.prefs"];
        NSDictionary *visibleSR = dictationPrefs[@"VisibleNetworkSRLocaleIdentifiers"];

        NSSet<NSLocale *> *supported = SFSpeechRecognizer.supportedLocales;
        NSMutableArray *list = [NSMutableArray array];

        for (NSLocale *loc in supported) {
            SFSpeechRecognizer *rec = [[SFSpeechRecognizer alloc] initWithLocale:loc];
            BOOL onDevice = rec.supportsOnDeviceRecognition;
            NSString *locId = loc.localeIdentifier;
            NSString *locIdUnderscore = [locId stringByReplacingOccurrencesOfString:@"-" withString:@"_"];
            BOOL isDictation = (visibleSR[locId] != nil || visibleSR[locIdUnderscore] != nil);

            NSString *displayName = [userLocale localizedStringForLocaleIdentifier:locId];
            if (!displayName.length) displayName = locId;
            if (displayName.length > 1) {
                displayName = [displayName stringByReplacingCharactersInRange:NSMakeRange(0,1) withString:[[displayName substringToIndex:1] uppercaseString]];
            }

            [list addObject:[NSMutableDictionary dictionaryWithDictionary:@{
                @"identifier": locId,
                @"displayName": displayName,
                @"isOnDevice": @(onDevice),
                @"isDictation": @(isDictation),
                @"isDefault": @(NO)
            }]];
            [rec release];
        }

        [list sortUsingComparator:^NSComparisonResult(NSDictionary *a, NSDictionary *b) {
            BOOL aOnDevice = [a[@"isOnDevice"] boolValue];
            BOOL bOnDevice = [b[@"isOnDevice"] boolValue];
            BOOL aDict = [a[@"isDictation"] boolValue];
            BOOL bDict = [b[@"isDictation"] boolValue];

            if (aDict != bDict) {
                if (aDict && aOnDevice) return NSOrderedAscending;
                if (bDict && bOnDevice) return NSOrderedDescending;
            }
            if (aOnDevice != bOnDevice) {
                return aOnDevice ? NSOrderedAscending : NSOrderedDescending;
            }
            if (aDict != bDict) {
                return aDict ? NSOrderedAscending : NSOrderedDescending;
            }
            return [a[@"displayName"] compare:b[@"displayName"]];
        }];

        NSString *bestDefaultId = nil;
        for (NSMutableDictionary *item in list) {
            NSString *idStr = item[@"identifier"];
            if ([item[@"isOnDevice"] boolValue] && [idStr hasPrefix:kbdLang ?: @""]) {
                bestDefaultId = idStr;
                break;
            }
        }
        if (!bestDefaultId) {
            for (NSMutableDictionary *item in list) {
                NSString *idStr = item[@"identifier"];
                if ([item[@"isOnDevice"] boolValue] && [idStr hasPrefix:userLocale.languageCode ?: @""]) {
                    bestDefaultId = idStr;
                    break;
                }
            }
        }
        if (!bestDefaultId && list.count > 0) {
            bestDefaultId = list[0][@"identifier"];
        }

        for (NSMutableDictionary *item in list) {
            if ([item[@"identifier"] isEqualToString:bestDefaultId]) {
                item[@"isDefault"] = @(YES);
                break;
            }
        }

        NSData *jsonData = [NSJSONSerialization dataWithJSONObject:list options:0 error:nil];
        if (!jsonData) return strdup("[]");
        NSString *jsonString = [[[NSString alloc] initWithData:jsonData encoding:NSUTF8StringEncoding] autorelease];
        return strdup(jsonString.UTF8String ?: "[]");
    }
}

static int LoomSpeechTranscriptionAvailable(void) {
    if (!LoomHasSpeechUsageDescription()) return 0;
	if (@available(macOS 10.15, *)) {
		return SFSpeechRecognizer.authorizationStatus != SFSpeechRecognizerAuthorizationStatusDenied
            && SFSpeechRecognizer.authorizationStatus != SFSpeechRecognizerAuthorizationStatusRestricted;
    }
    return 0;
}

static char *LoomTranscribeAudio(const char *path, const char *localeIdentifier, int allowNetwork, char **errorOut) {
    @autoreleasepool {
        if (!LoomHasSpeechUsageDescription()) {
            *errorOut = LoomSpeechError(@"The app bundle is missing its speech recognition usage description");
            return NULL;
        }
        __block SFSpeechRecognizerAuthorizationStatus authorization = SFSpeechRecognizer.authorizationStatus;
        if (authorization == SFSpeechRecognizerAuthorizationStatusNotDetermined) {
            dispatch_semaphore_t authorizationDone = dispatch_semaphore_create(0);
            [SFSpeechRecognizer requestAuthorization:^(SFSpeechRecognizerAuthorizationStatus status) {
                authorization = status;
                dispatch_semaphore_signal(authorizationDone);
            }];
            if (dispatch_semaphore_wait(authorizationDone, dispatch_time(DISPATCH_TIME_NOW, 60 * NSEC_PER_SEC)) != 0) {
                *errorOut = LoomSpeechError(@"Speech recognition permission timed out");
                return NULL;
            }
        }
        if (authorization != SFSpeechRecognizerAuthorizationStatusAuthorized) {
            *errorOut = LoomSpeechError(@"Speech recognition permission was not granted");
            return NULL;
        }

		SFSpeechRecognizer *recognizer = [[SFSpeechRecognizer alloc] initWithLocale:LoomResolveSpeechLocale(localeIdentifier)];
		if (!recognizer.isAvailable) {
			[recognizer release];
			*errorOut = LoomSpeechError(@"Speech recognition is unavailable. Enable Dictation in System Settings > Keyboard, then try again.");
			return NULL;
		}
		if (!recognizer.supportsOnDeviceRecognition && !allowNetwork) {
            [recognizer release];
			*errorOut = LoomSpeechError(@"network speech recognition consent required");
            return NULL;
        }

        NSURL *url = [NSURL fileURLWithPath:[NSString stringWithUTF8String:path]];
		SFSpeechURLRecognitionRequest *request = [[SFSpeechURLRecognitionRequest alloc] initWithURL:url];
		request.requiresOnDeviceRecognition = recognizer.supportsOnDeviceRecognition;
		request.shouldReportPartialResults = YES;
		request.taskHint = SFSpeechRecognitionTaskHintUnspecified;
		if (@available(macOS 13, *)) {
			request.addsPunctuation = YES;
		}

        dispatch_semaphore_t done = dispatch_semaphore_create(0);
        __block NSString *recognizedText = nil;
        __block NSError *recognitionError = nil;
        __block BOOL finished = NO;
		SFSpeechRecognitionTask *task = [recognizer recognitionTaskWithRequest:request resultHandler:^(SFSpeechRecognitionResult *result, NSError *error) {
			@synchronized (request) {
				if (finished) return;
				NSString *partialText = result.bestTranscription.formattedString;
				if (partialText.length) {
					[recognizedText release];
					recognizedText = [partialText copy];
				}
				if (error || result.isFinal) {
					finished = YES;
					recognitionError = [error retain];
                    dispatch_semaphore_signal(done);
                }
            }
        }];

        if (dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 120 * NSEC_PER_SEC)) != 0) {
            [task cancel];
            *errorOut = LoomSpeechError(@"Speech recognition timed out");
		} else if (recognitionError) {
			NSString *message = recognitionError.localizedDescription;
			if ([recognitionError.domain isEqualToString:@"kAFAssistantErrorDomain"] && recognitionError.code == 1101) {
				message = @"Enable Dictation in System Settings > Keyboard, then try again.";
			}
			*errorOut = LoomSpeechError(message);
        }

        char *result = recognizedText.length ? strdup(recognizedText.UTF8String) : NULL;
		if (!result && !*errorOut) {
			NSString *language = [NSLocale.currentLocale localizedStringForLocaleIdentifier:recognizer.locale.localeIdentifier];
			*errorOut = LoomSpeechError([NSString stringWithFormat:@"No speech was recognized using %@. Check the language under System Settings > Keyboard > Dictation Languages.", language]);
		}
        [recognizedText release];
        [recognitionError release];
        [request release];
        [recognizer release];
        return result;
    }
}
*/
import "C"

import (
	"encoding/json"
	"errors"
	"os"
	"unsafe"
)

type SpeechLocale struct {
	Identifier   string `json:"identifier"`
	DisplayName  string `json:"displayName"`
	IsOnDevice   bool   `json:"isOnDevice"`
	IsDictation  bool   `json:"isDictation"`
	IsDefault    bool   `json:"isDefault"`
}

func speechTranscriptionAvailable() bool {
	// macOS attributes privacy prompts from `wails dev` to its launcher (for
	// example VS Code) and aborts Loom because that bundle has no usage string.
	if os.Getenv("devserver") != "" {
		return false
	}
	return C.LoomSpeechTranscriptionAvailable() != 0
}

func getSpeechTranscriptionLocales() []SpeechLocale {
	if !speechTranscriptionAvailable() {
		return nil
	}
	cJSON := C.LoomGetSpeechLocalesJSON()
	if cJSON == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(cJSON))
	var locales []SpeechLocale
	if err := json.Unmarshal([]byte(C.GoString(cJSON)), &locales); err != nil {
		return nil
	}
	return locales
}

func transcribeAudioFile(path string, localeID string, allowNetwork bool) (string, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	var cLocale *C.char
	if localeID != "" {
		cLocale = C.CString(localeID)
		defer C.free(unsafe.Pointer(cLocale))
	}
	var cError *C.char
	cAllowNetwork := C.int(0)
	if allowNetwork {
		cAllowNetwork = 1
	}
	result := C.LoomTranscribeAudio(cPath, cLocale, cAllowNetwork, &cError)
	if result != nil {
		defer C.free(unsafe.Pointer(result))
		return C.GoString(result), nil
	}
	if cError != nil {
		defer C.free(unsafe.Pointer(cError))
		return "", errors.New(C.GoString(cError))
	}
	return "", errors.New("speech recognition failed")
}

