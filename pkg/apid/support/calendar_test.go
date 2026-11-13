package support

import (
	"testing"
	"time"
)

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, e := time.Parse(time.RFC3339, s)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestBusinessCalendarDSTAndHolidays(t *testing.T) {
	c := Calendar{Timezone: "America/New_York", Weekdays: []int{0}, StartMinute: 60, EndMinute: 240}
	for _, tc := range []struct {
		name, start, end string
		budget           time.Duration
	}{
		{"spring", "2026-03-08T06:00:00Z", "2026-03-08T08:00:00Z", 2 * time.Hour},
		{"fall", "2026-11-01T05:00:00Z", "2026-11-01T09:00:00Z", 4 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := at(t, tc.start)
			want := at(t, tc.end)
			got, e := c.deadline(start, tc.budget)
			if e != nil || !got.Equal(want) {
				t.Fatalf("deadline %v %v, want %v", got, e, want)
			}
			if got := c.working(start, want); got != tc.budget {
				t.Fatalf("working %v want %v", got, tc.budget)
			}
		})
	}
	c = Calendar{Timezone: "UTC", Weekdays: []int{1, 2, 3, 4, 5}, StartMinute: 9 * 60, EndMinute: 17 * 60, Holidays: []string{"2026-11-09"}}
	due, e := c.deadline(at(t, "2026-11-06T16:00:00Z"), 2*time.Hour)
	if e != nil || !due.Equal(at(t, "2026-11-10T10:00:00Z")) {
		t.Fatalf("holiday deadline: %v %v", due, e)
	}
}
func TestWaitingPausesResolutionAndPreservesBreaches(t *testing.T) {
	now := at(t, "2026-11-11T10:00:00Z")
	p := defaultPolicy()
	p.ResolutionSeconds = 3600
	e, err := newEpisode(p, "pro", "normal", "other", now)
	if err != nil {
		t.Fatal(err)
	}
	if e.pause(now) == nil {
		t.Fatal("waiting without reply was allowed")
	}
	e.reply(now)
	if err = e.pause(now.Add(30 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = e.resolve(now.Add(48 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if e.ResolutionBreached || e.PausedUS != int64((48*time.Hour-30*time.Minute)/time.Microsecond) {
		t.Fatalf("paused resolution: %+v", e)
	}
	late, _ := newEpisode(p, "pro", "normal", "other", now)
	late.reply(now)
	if err = late.pause(now.Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = late.resolve(now.Add(3 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !late.ResolutionBreached {
		t.Fatal("waiting erased a breach")
	}
}
func TestRepeatedCustomerRepliesKeepOldestDeadline(t *testing.T) {
	now := at(t, "2026-11-11T10:00:00Z")
	e, _ := newEpisode(defaultPolicy(), "pro", "normal", "other", now)
	e.reply(now)
	if err := e.customerReply(now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	due := *e.NextReplyDue
	if err := e.customerReply(now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !e.NextReplyDue.Equal(due) {
		t.Fatal("customer reply extended outstanding response deadline")
	}
	e.reply(due)
	if e.NextReplyMet != 1 {
		t.Fatal("reply exactly on deadline should meet target")
	}
}

func TestBusinessCalendarBoundariesInsideDSTAndSkippedDate(t *testing.T) {
	c := Calendar{Timezone: "America/New_York", Weekdays: []int{0}, StartMinute: 120, EndMinute: 240}
	start := at(t, "2026-03-08T06:00:00Z")
	due, e := c.deadline(start, time.Hour)
	if e != nil || !due.Equal(at(t, "2026-03-08T08:00:00Z")) {
		t.Fatalf("nonexistent start: %v %v", due, e)
	}
	if c.working(start, due) != time.Hour {
		t.Fatal("missing hour counted as working time")
	}
	c.StartMinute = 60
	c.EndMinute = 90
	start = at(t, "2026-11-01T04:00:00Z")
	due, e = c.deadline(start, time.Hour)
	if e != nil || !due.Equal(at(t, "2026-11-01T06:30:00Z")) {
		t.Fatalf("repeated partial window: %v %v", due, e)
	}
	if c.working(start, due) != time.Hour {
		t.Fatal("repeated half-hour window did not count both occurrences")
	}
	c = Calendar{Timezone: "Pacific/Apia", Weekdays: []int{0, 1, 2, 3, 4, 5, 6}, StartMinute: 0, EndMinute: 1440}
	start = at(t, "2011-12-30T09:30:00Z")
	due, e = c.deadline(start, time.Hour)
	if e != nil || !due.Equal(start.Add(time.Hour)) {
		t.Fatalf("skipped civil date: %v %v", due, e)
	}
}
