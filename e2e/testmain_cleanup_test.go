package e2e

import (
	"errors"
	"testing"
	"time"
)

func TestRemoveAllWithRetryRecoversFromTransientFailure(t *testing.T) {
	transient := errors.New("directory not empty")
	calls := 0
	err := removeAllWithRetry("sandbox", 3, 0, func(string) error {
		calls++
		if calls < 3 {
			return transient
		}
		return nil
	})
	if err != nil {
		t.Fatalf("removeAllWithRetry: %v", err)
	}
	if calls != 3 {
		t.Fatalf("remove calls = %d, want 3", calls)
	}
}

func TestRemoveAllWithRetryReturnsLastFailure(t *testing.T) {
	want := errors.New("still busy")
	calls := 0
	err := removeAllWithRetry("sandbox", 2, time.Nanosecond, func(string) error {
		calls++
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if calls != 2 {
		t.Fatalf("remove calls = %d, want 2", calls)
	}
}
