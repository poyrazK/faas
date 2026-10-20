// adapter_apid_pgtype_mega5_test.go — Coverage Mega-PR #5 cluster 8a:
// pin NewPgtypeTime (pkg/state/adapter_apid_pgtype.go:73) at 100%. The
// existing test only exercised the non-zero branch; the zero-time → NULL
// branch was uncovered (66.7%).
//
// Whitebox `package state`. No Postgres dependency — runs on the
// unit-tests-pure-* CI lanes.

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestNewPgtypeTime_Zero_Mega5(t *testing.T) {
	t.Parallel()
	got := state.NewPgtypeTime(time.Time{})
	if got.Valid {
		t.Errorf("zero time: Valid = true, want false (NULL)")
	}
	if !got.Time.IsZero() {
		t.Errorf("zero time: Time = %v, want zero", got.Time)
	}
}

func TestNewPgtypeTime_NonZeroUTC_Mega5(t *testing.T) {
	t.Parallel()
	// Pass a non-UTC time to pin the UTC-normalisation branch.
	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skipf("Asia/Tokyo unavailable: %v", err)
	}
	inUTC := time.Date(2026, 8, 24, 12, 0, 0, 0, loc)
	got := state.NewPgtypeTime(inUTC)
	if !got.Valid {
		t.Fatal("non-zero time: Valid = false, want true")
	}
	if got.Time.Location() != time.UTC {
		t.Errorf("Location = %v, want UTC", got.Time.Location())
	}
}
