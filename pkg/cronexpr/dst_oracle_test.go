package cronexpr

import (
	"fmt"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
)

// The oracle walks every absolute minute and applies DSTPolicy literally, so
// it holds for any transition size: whole hours, Lord_Howe's 30 minutes or
// Troll's two hours.
//
//   - interval: fire at every instant whose wall time matches, so a repeated
//     wall time fires on both passes and a skipped one never fires.
//   - fixed_wall_time: fire at the first instant of each matching wall time;
//     a matching wall time inside a spring-forward gap fires once at the first
//     instant after the gap.
func oracleFires(spec *cron.SpecSchedule, loc *time.Location, fixed bool, from, to time.Time) []time.Time {
	var out []time.Time
	from = from.Truncate(time.Minute)
	for x := from.Add(time.Minute); !x.After(to); x = x.Add(time.Minute) {
		_, off := x.In(loc).Zone()
		_, prevOff := x.Add(-time.Minute).In(loc).Zone()
		if fixed && off > prevOff && skippedWallMatches(spec, x, prevOff, off) {
			out = append(out, x)
			continue
		}
		w := x.In(loc)
		if !wallMatches(spec, w) {
			continue
		}
		if fixed && wallSeenBefore(x, loc) {
			continue
		}
		out = append(out, x)
	}
	return out
}

func wallMatches(spec *cron.SpecSchedule, w time.Time) bool {
	return spec.Month&(1<<uint(w.Month())) != 0 && spec.Hour&(1<<uint(w.Hour())) != 0 &&
		spec.Minute&(1<<uint(w.Minute())) != 0 && dayMatches(spec, w)
}

// skippedWallMatches reports whether a wall minute swallowed by the jump at
// instant x (offset before -> after) matches the spec.
func skippedWallMatches(spec *cron.SpecSchedule, x time.Time, before, after int) bool {
	start := x.In(time.FixedZone("", before))
	for m := 0; m < (after-before)/60; m++ {
		if wallMatches(spec, start.Add(time.Duration(m)*time.Minute)) {
			return true
		}
	}
	return false
}

// wallSeenBefore reports whether x's wall minute already occurred at an
// earlier instant (the second pass through a repeated fall-back period).
func wallSeenBefore(x time.Time, loc *time.Location) bool {
	w := x.In(loc)
	for back := time.Minute; back <= 3*time.Hour; back += time.Minute {
		if sameWallMinute(x.Add(-back).In(loc), w) {
			return true
		}
	}
	return false
}

// transitions returns the instants in [from, to) where loc's offset changes.
func transitions(loc *time.Location, from, to time.Time) []time.Time {
	var out []time.Time
	for day := from; day.Before(to); day = day.Add(24 * time.Hour) {
		next := day.Add(24 * time.Hour)
		_, a := day.In(loc).Zone()
		if change, ok := firstOffsetChange(day, next, loc, a); ok {
			out = append(out, change)
		}
	}
	return out
}

func scheduleFires(s cron.Schedule, from, to time.Time) []time.Time {
	var out []time.Time
	for n := s.Next(from); !n.IsZero() && !n.After(to); n = s.Next(n) {
		out = append(out, n)
		if len(out) > 20000 {
			break
		}
	}
	return out
}

var dstOracleZones = []string{
	"UTC", "America/New_York", "Europe/London", "Europe/Dublin", "America/Santiago",
	"America/Sao_Paulo", "America/Havana", "America/St_Johns", "Asia/Gaza", "Asia/Tehran",
	"Asia/Kolkata", "Asia/Kathmandu", "Australia/Adelaide", "Australia/Eucla",
	"Australia/Lord_Howe", "Pacific/Chatham", "Antarctica/Troll",
}

var dstOracleExpressions = []string{
	// fixed wall times
	"30 2 * * *", "0 1 * * *", "15 0 * * *", "45 1 * * *", "0 2 * * *", "59 23 * * *",
	"30 1,2 * * *", "0-59 1 * * *", "15 2 * * 0", "0 0 1 * *",
	// intervals
	"0 * * * *", "*/15 * * * *", "*/7 * * * *", "30 * * * *", "* 1 * * *",
}

// Every zone's 2026 transitions, two days either side, plus a plain week: the
// computed schedule must match the brute-force oracle fire for fire.
func TestNextMatchesDSTOracleAcrossZones(t *testing.T) {
	year := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, zone := range dstOracleZones {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatalf("%s: %v", zone, err)
		}
		windows := [][2]time.Time{{year.AddDate(0, 6, 0), year.AddDate(0, 6, 7)}}
		for _, tr := range transitions(loc, year, year.AddDate(1, 0, 0)) {
			windows = append(windows, [2]time.Time{tr.Add(-48 * time.Hour), tr.Add(48 * time.Hour)})
		}
		for _, expr := range dstOracleExpressions {
			schedule, err := Parse(expr, zone)
			if err != nil {
				t.Fatalf("%s %q: %v", zone, expr, err)
			}
			spec, err := parser.Parse("CRON_TZ=" + zone + " " + expr)
			if err != nil {
				t.Fatal(err)
			}
			for _, window := range windows {
				want := oracleFires(spec.(*cron.SpecSchedule), loc, fixedWallTime(expr), window[0], window[1])
				got := scheduleFires(schedule, window[0], window[1])
				if diff := firstFireDiff(got, want, loc); diff != "" {
					t.Errorf("%s %q from %s: %s", zone, expr, window[0].In(loc).Format(time.RFC3339), diff)
				}
			}
		}
	}
}

func firstFireDiff(got, want []time.Time, loc *time.Location) string {
	for i := 0; i < len(got) || i < len(want); i++ {
		switch {
		case i >= len(got):
			return fmt.Sprintf("missing fire %d at %s", i, want[i].In(loc).Format(time.RFC3339))
		case i >= len(want):
			return fmt.Sprintf("extra fire %d at %s", i, got[i].In(loc).Format(time.RFC3339))
		case !got[i].Equal(want[i]):
			return fmt.Sprintf("fire %d at %s, want %s", i, got[i].In(loc).Format(time.RFC3339), want[i].In(loc).Format(time.RFC3339))
		}
	}
	return ""
}

// NominalOccurrence labels a computed fire that is not a plain wall-clock
// match as a DST shift. It must agree with the wall clock in every zone; in
// Lord_Howe robfig's own Next made ordinary fires look shifted.
func TestNominalOccurrenceMatchesWallClock(t *testing.T) {
	year := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, zone := range []string{"America/New_York", "Australia/Lord_Howe", "Antarctica/Troll"} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		for _, tr := range transitions(loc, year, year.AddDate(1, 0, 0)) {
			for _, expr := range []string{"15 0 * * *", "0 2 * * *", "45 1 * * *", "*/15 * * * *"} {
				spec, err := parser.Parse("CRON_TZ=" + zone + " " + expr)
				if err != nil {
					t.Fatal(err)
				}
				for x := tr.Add(-30 * time.Hour); x.Before(tr.Add(30 * time.Hour)); x = x.Add(time.Minute) {
					got, err := NominalOccurrence(expr, zone, x)
					if err != nil {
						t.Fatal(err)
					}
					if want := wallMatches(spec.(*cron.SpecSchedule), x.In(loc)); got != want {
						t.Fatalf("%s %q at %s: nominal=%v, want %v", zone, expr, x.In(loc).Format(time.RFC3339), got, want)
					}
				}
			}
		}
	}
}
