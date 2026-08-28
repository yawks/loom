package db

import (
	"errors"
	"fmt"
	"testing"
)

type sqliteCodeError int

func (err sqliteCodeError) Error() string { return fmt.Sprintf("sqlite error (%d)", err) }
func (err sqliteCodeError) Code() int     { return int(err) }

func TestIsSQLiteBusy(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "busy", err: sqliteCodeError(5), want: true},
		{name: "busy snapshot", err: sqliteCodeError(517), want: true},
		{name: "wrapped busy snapshot", err: fmt.Errorf("store messages: %w", sqliteCodeError(517)), want: true},
		{name: "fallback driver message", err: errors.New("database is locked (517)"), want: true},
		{name: "constraint", err: sqliteCodeError(19), want: false},
		{name: "other", err: errors.New("network unavailable"), want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isSQLiteBusy(test.err); got != test.want {
				t.Fatalf("isSQLiteBusy(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}
