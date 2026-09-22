// spec: §10 — billing aggregation must fail closed and retain retry state.
package meter

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestOverageCursorFillsOnlyGaps(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	cursor := overageCursor{nextHour: start}
	reads := 0
	sum := func(from, to time.Time) (int64, error) {
		reads++
		if !from.Equal(start.Add(2*time.Hour)) || !to.Equal(start.Add(4*time.Hour)) {
			t.Fatalf("gap = [%s, %s), want exactly the two skipped hours", from, to)
		}
		return 30, nil
	}
	for _, tc := range []struct {
		hour          int
		before, after int64
	}{
		{0, 0, 10}, {1, 10, 20}, {4, 50, 60},
	} {
		before, after, err := cursor.advance(start.Add(time.Duration(tc.hour)*time.Hour), 10, sum)
		if err != nil || before != tc.before || after != tc.after {
			t.Fatalf("hour %d: advance = (%d, %d, %v)", tc.hour, before, after, err)
		}
	}
	if reads != 1 {
		t.Fatalf("gap reads = %d, want one; contiguous hours must reuse the cursor", reads)
	}
}

func TestOverageCursorFailureDoesNotAdvance(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name          string
		raw, gap, qty int64
		readErr       error
	}{
		{"gap read", 1, 0, 1, errors.New("usage read failed")},
		{"gap overflow", math.MaxInt64, 1, 1, nil},
		{"window overflow", math.MaxInt64 - 1, 1, 1, nil},
		{"negative gap", 1, -1, 1, nil},
		{"negative window", 1, 1, -1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cursor := overageCursor{rawBefore: tc.raw, nextHour: start}
			before := cursor
			_, _, err := cursor.advance(start.Add(time.Hour), tc.qty, func(time.Time, time.Time) (int64, error) { return tc.gap, tc.readErr })
			if err == nil || cursor != before {
				t.Fatalf("advance = (%+v, %v), want unchanged %+v and error", cursor, err, before)
			}
		})
	}
	// Duplicate/out-of-order hours must not add usage a second time.
	cursor := overageCursor{nextHour: start.Add(time.Hour)}
	if _, _, err := cursor.advance(start, 1, nil); err == nil {
		t.Fatal("accepted out-of-order billing window")
	}
}

func TestOverageCursorPartialFirstHour(t *testing.T) {
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	start := month.Add(30 * time.Minute)
	cursor := overageCursor{nextHour: month}
	before, after, err := cursor.advance(start, 10, func(from, to time.Time) (int64, error) {
		if !from.Equal(month) || !to.Equal(start) {
			t.Fatalf("prefix = [%s,%s), want [%s,%s)", from, to, month, start)
		}
		return 50, nil
	})
	if err != nil || before != 50 || after != 60 || !cursor.nextHour.Equal(month.Add(time.Hour)) {
		t.Fatalf("first hour = (%d, %d, %v), cursor=%+v", before, after, err, cursor)
	}
	before, after, err = cursor.advance(month.Add(time.Hour), 10, func(time.Time, time.Time) (int64, error) {
		t.Fatal("contiguous hour re-read usage after a partial first hour")
		return 0, nil
	})
	if err != nil || before != 60 || after != 70 {
		t.Fatalf("second hour = (%d, %d, %v)", before, after, err)
	}
}
