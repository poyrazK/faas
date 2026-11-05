// adr: 20261026120843
package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeSpendCapReader struct {
	capCents, observed int64
	capped             bool
	capErr             error
}

func (f fakeSpendCapReader) GetAccountOverageCapCents(context.Context, string) (int64, bool, error) {
	return f.capCents, f.capped, f.capErr
}

func (f fakeSpendCapReader) CurrentMonthOverageCents(context.Context, string) (int64, error) {
	return f.observed, nil
}

type fakeSpendCapHoldReader struct {
	fakeSpendCapReader
	hold SpendCapHold
}

func (f fakeSpendCapHoldReader) PutSpendCapHold(context.Context, SpendCapHold) error { return nil }
func (f fakeSpendCapHoldReader) ClearSpendCapHold(context.Context, string) error     { return nil }
func (f fakeSpendCapHoldReader) GetSpendCapHold(context.Context, string) (SpendCapHold, bool, error) {
	return f.hold, true, nil
}

// SpendCapStatusFor is the one spend cap decision schedd's wake gate, its
// cron sweep and apid's manual cron runs share (crons audit 2026-11-05).
func TestSpendCapStatusFor(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		store SpendCapReader
		want  SpendCapStatus
		err   bool
	}{
		{"no cap", fakeSpendCapReader{observed: 900}, SpendCapStatus{}, false},
		{"under the cap", fakeSpendCapReader{capped: true, capCents: 1000, observed: 400},
			SpendCapStatus{Capped: true, CapCents: 1000, ObservedCents: 400}, false},
		{"at the cap", fakeSpendCapReader{capped: true, capCents: 1000, observed: 1000},
			SpendCapStatus{Capped: true, Reached: true, CapCents: 1000, ObservedCents: 1000}, false},
		{"meterd hold this month", fakeSpendCapHoldReader{
			fakeSpendCapReader: fakeSpendCapReader{capped: true, capCents: 1000, observed: 400},
			hold:               SpendCapHold{Month: month, CapCents: 1000, ObservedCents: 1200},
		}, SpendCapStatus{Capped: true, Reached: true, CapCents: 1000, ObservedCents: 1200}, false},
		{"hold for an older cap", fakeSpendCapHoldReader{
			fakeSpendCapReader: fakeSpendCapReader{capped: true, capCents: 2000, observed: 400},
			hold:               SpendCapHold{Month: month, CapCents: 1000, ObservedCents: 1200},
		}, SpendCapStatus{Capped: true, CapCents: 2000, ObservedCents: 400}, false},
		{"cap read fails open", fakeSpendCapReader{capErr: errors.New("db down")}, SpendCapStatus{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SpendCapStatusFor(t.Context(), tc.store, "acct", now)
			if (err != nil) != tc.err || got != tc.want {
				t.Fatalf("status = %+v err=%v, want %+v err=%v", got, err, tc.want, tc.err)
			}
		})
	}
}
