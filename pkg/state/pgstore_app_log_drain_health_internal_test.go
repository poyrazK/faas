package state

import (
	"errors"
	"testing"
	"time"
)

type appLogDrainHealthScanRowStub struct {
	err error
}

func (r appLogDrainHealthScanRowStub) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*string) = "drain-id"
	*dest[1].(*string) = "degraded"
	*dest[2].(*bool) = true
	*dest[3].(*int) = 4
	*dest[4].(*int) = 32
	*dest[5].(*int) = 3
	*dest[6].(*int64) = 2048
	*dest[7].(*int64) = 65536
	*dest[8].(*int64) = 1
	*dest[9].(**time.Time) = nil
	*dest[10].(*int64) = 10
	*dest[11].(*int64) = 2
	*dest[12].(*int64) = 1
	*dest[13].(*int64) = 3
	*dest[14].(*int64) = 2
	*dest[15].(*int64) = 1
	*dest[16].(**time.Time) = nil
	*dest[17].(**time.Time) = nil
	*dest[18].(*string) = "source log gap observed"
	*dest[19].(*time.Time) = time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)
	return nil
}

func TestAppLogDrainHealthHelpers(t *testing.T) {
	if got := healthStatus(""); got != "unknown" {
		t.Fatalf("healthStatus empty = %q, want unknown", got)
	}
	if got := healthStatus("healthy"); got != "healthy" {
		t.Fatalf("healthStatus healthy = %q, want healthy", got)
	}

	got, err := scanAppLogDrainHealth(appLogDrainHealthScanRowStub{})
	if err != nil {
		t.Fatalf("scanAppLogDrainHealth: %v", err)
	}
	if got.DrainID != "drain-id" || got.Status != "degraded" || !got.Active || got.QueueDepth != 4 || got.LastError != "source log gap observed" || !got.UpdatedAt.Equal(time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("scanned health = %+v", got)
	}
	if !got.LastSuccessAt.IsZero() || !got.LastFailureAt.IsZero() {
		t.Fatalf("nil timestamps became non-zero: %+v", got)
	}

	scanErr := errors.New("scan failed")
	if _, err := scanAppLogDrainHealth(appLogDrainHealthScanRowStub{err: scanErr}); !errors.Is(err, scanErr) {
		t.Fatalf("scanAppLogDrainHealth error = %v, want %v", err, scanErr)
	}
}
