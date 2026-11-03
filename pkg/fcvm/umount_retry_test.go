// adr: 005 — a failed restore must unmount cleanly so the cold-boot fallback always works.
package fcvm

import (
	"errors"
	"testing"
	"time"
)

func TestRetryUmountWhileMounted_RetriesTransientBusy(t *testing.T) {
	t.Parallel()
	attempts := 0
	mounted := true
	err := retryUmountWhileMounted("/mp",
		func(string) error {
			attempts++
			if attempts < 3 {
				return errors.New("exit status 32")
			}
			mounted = false
			return nil
		},
		func(string) (bool, error) { return mounted, nil },
		func(time.Duration) {}, 25*time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("err = %v, want nil after the holder released the mount", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestRetryUmountWhileMounted_GivesUpAfterBudget(t *testing.T) {
	t.Parallel()
	attempts := 0
	busy := errors.New("exit status 32")
	err := retryUmountWhileMounted("/mp",
		func(string) error { attempts++; return busy },
		func(string) (bool, error) { return true, nil },
		func(time.Duration) {}, 25*time.Millisecond, 100*time.Millisecond)
	if !errors.Is(err, busy) {
		t.Fatalf("err = %v, want the umount error once the budget is spent", err)
	}
	if attempts != 5 {
		t.Fatalf("attempts = %d, want 5 (0..100ms in 25ms steps)", attempts)
	}
}

// A loaded node released the loop device's reference after more than a second
// (2026-10-29); the production budget must outlast that.
func TestRetryUmountWhileMounted_ProductionBudgetOutlastsSlowRelease(t *testing.T) {
	t.Parallel()
	var elapsed time.Duration
	const holder = 2 * time.Second
	err := retryUmountWhileMounted("/mp",
		func(string) error {
			if elapsed < holder {
				return errors.New("exit status 32: umount: /mp: target is busy.")
			}
			return nil
		},
		func(string) (bool, error) { return true, nil },
		func(d time.Duration) { elapsed += d }, umountBusyRetryInterval, umountBusyRetryBudget)
	if err != nil {
		t.Fatalf("err = %v, want nil once a %s holder releases the mount", err, holder)
	}
}

func TestRetryUmountWhileMounted_AlreadyGoneIsSuccess(t *testing.T) {
	t.Parallel()
	err := retryUmountWhileMounted("/mp",
		func(string) error { return errors.New("not mounted") },
		func(string) (bool, error) { return false, nil },
		func(time.Duration) { t.Fatal("must not sleep when the mount is gone") }, time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("err = %v, want nil when nothing is mounted", err)
	}
}
