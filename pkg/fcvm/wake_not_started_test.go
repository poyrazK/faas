// adr: 20261022184531
package fcvm

import (
	"errors"
	"fmt"
	"testing"
)

func TestWakeNotStartedKeepsTheErrorAndItsText(t *testing.T) {
	inner := errors.New("netns add: file exists")
	err := wakeNotStarted(fmt.Errorf("wake i: network setup: %w", inner))
	if !errors.Is(err, ErrWakeNotStarted) || !errors.Is(err, inner) || err.Error() != "wake i: network setup: netns add: file exists" {
		t.Fatalf("wrapped = %q (not started %t, inner %t)", err, errors.Is(err, ErrWakeNotStarted), errors.Is(err, inner))
	}
	if wakeNotStarted(nil) != nil {
		t.Fatal("wrapped a nil error")
	}
}
