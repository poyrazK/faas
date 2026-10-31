// adr: 1162
package billing

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// overageRangeStore serves minute usage and per-month price activations. Only
// the two methods OverageMBSecondsForRange calls are implemented.
type overageRangeStore struct {
	state.FinancialStore
	minutes   map[time.Time]int64
	snapshots []state.FinancialPriceSnapshot
}

func (s *overageRangeStore) UsageByHour(_ context.Context, _ string, start, end time.Time) ([]state.Usage, error) {
	var total int64
	for minute, mbSeconds := range s.minutes {
		if !minute.Before(start) && minute.Before(end) {
			total += mbSeconds
		}
	}
	return []state.Usage{{MBSeconds: total}}, nil
}

func (s *overageRangeStore) ListFinancialPriceSnapshots(_ context.Context, _ string, month time.Time) ([]state.FinancialPriceSnapshot, error) {
	var out []state.FinancialPriceSnapshot
	for _, snapshot := range s.snapshots {
		if snapshot.PeriodStart.Equal(month) {
			out = append(out, snapshot)
		}
	}
	return out, nil
}

// Overage over a range is the sum of what the hourly pusher delivers, so it
// must be additive: splitting [a, c) at any b, including mid-hour, across a
// month end and across plan activations, gives the same total. A split that
// re-applies or skips an allowance would bill a customer twice or not at all
// for the same usage.
func FuzzOverageRangeIsAdditive(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, uint16(100), uint16(2000), uint16(4000))
	f.Add([]byte{255, 0, 255, 0, 200}, uint16(1430), uint16(1450), uint16(2900))
	f.Fuzz(func(t *testing.T, data []byte, a, b, c uint16) {
		// A four-day window centred on the January/February month end.
		origin := time.Date(2026, 1, 30, 0, 0, 0, 0, time.UTC)
		const windowMinutes = 4 * 24 * 60
		store := &overageRangeStore{minutes: map[time.Time]int64{}}
		for i := 0; i+1 < len(data) && i < 400; i += 2 {
			minute := origin.Add(time.Duration(int(data[i])*int(data[i+1])%windowMinutes) * time.Minute)
			store.minutes[minute] += int64(data[i]) * 10_000_000
		}
		for i := 0; i+2 < len(data) && i < 24; i += 3 {
			effective := origin.Add(time.Duration(int(data[i])*int(data[i+1])%windowMinutes) * time.Minute).Add(time.Duration(data[i+2]) * time.Second)
			month := utcMonthStart(effective)
			store.snapshots = append(store.snapshots, state.FinancialPriceSnapshot{
				PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), EffectiveFrom: effective,
				Price: financial.Price{Meter: "compute", IncludedQuantity: int64(data[i+2]) * 50_000_000},
			})
		}
		points := []time.Time{
			origin.Add(time.Duration(int(a)%windowMinutes)*time.Minute + time.Duration(int(a)%60)*time.Second),
			origin.Add(time.Duration(int(b)%windowMinutes)*time.Minute + time.Duration(int(b)%60)*time.Second),
			origin.Add(time.Duration(int(c)%windowMinutes)*time.Minute + time.Duration(int(c)%60)*time.Second),
		}
		for i := 1; i < len(points); i++ {
			for j := i; j > 0 && points[j].Before(points[j-1]); j-- {
				points[j], points[j-1] = points[j-1], points[j]
			}
		}
		account := state.Account{ID: "acct-1", Plan: api.PlanHobby}
		ctx := context.Background()
		whole, err := OverageMBSecondsForRange(ctx, store, account, points[0], points[2])
		if err != nil {
			t.Fatal(err)
		}
		left, err := OverageMBSecondsForRange(ctx, store, account, points[0], points[1])
		if err != nil {
			t.Fatal(err)
		}
		right, err := OverageMBSecondsForRange(ctx, store, account, points[1], points[2])
		if err != nil {
			t.Fatal(err)
		}
		if whole < 0 || left < 0 || right < 0 {
			t.Fatalf("negative overage: whole=%d left=%d right=%d", whole, left, right)
		}
		if whole != left+right {
			t.Fatalf("overage not additive at split %s: [%s,%s)=%d but %d + %d",
				points[1].Format(time.RFC3339), points[0].Format(time.RFC3339), points[2].Format(time.RFC3339), whole, left, right)
		}
	})
}
