package screenprotection

import (
	"errors"
	"reflect"
	"testing"
)

func TestInitialDisableDoesNotRequireNativeWindow(t *testing.T) {
	var state protectionState
	for range 2 {
		if err := state.apply(false, func(bool) error {
			t.Fatal("startup attempted to access a native window")
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProtectionChangesStillReachNativeWindow(t *testing.T) {
	var state protectionState
	var calls []bool
	apply := func(enabled bool) error { calls = append(calls, enabled); return nil }
	// Repeated enables must still verify protection; only a known disable
	// can be skipped, including after a renderer reload.
	for _, enabled := range []bool{false, true, true, false, false} {
		if err := state.apply(enabled, apply); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(calls, []bool{true, true, false}) {
		t.Fatalf("native calls: %v", calls)
	}
}

func TestFailedNativeChangeRequiresSuccessfulDisable(t *testing.T) {
	var state protectionState
	nativeErr := errors.New("native verification failed")
	if err := state.apply(true, func(bool) error { return nativeErr }); !errors.Is(err, nativeErr) {
		t.Fatal(err)
	}
	for range 2 {
		if err := state.apply(false, func(bool) error { return nativeErr }); !errors.Is(err, nativeErr) {
			t.Fatalf("failed disable was ignored: %v", err)
		}
	}
	called := false
	if err := state.apply(false, func(bool) error { called = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !called || state.possiblyEnabled {
		t.Fatal("disable did not reset native state")
	}
}
