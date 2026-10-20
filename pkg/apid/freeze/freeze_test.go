package freeze

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261019071733 — which freezes withhold a production release, and when.

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func weekly(days int, start, minutes int, tz string) state.ReleaseFreeze {
	return state.ReleaseFreeze{Kind: state.ReleaseFreezeWeekly, WeeklyDays: days, WeeklyStartMinute: start,
		WeeklyDurationMinutes: minutes, TimeZone: tz}
}

func TestIsActive(t *testing.T) {
	start := mustTime(t, "2026-12-24T00:00:00Z")
	end := mustTime(t, "2026-12-27T00:00:00Z")
	lifted := mustTime(t, "2026-12-25T00:00:00Z")
	friEvening := weekly(1<<int(time.Friday), 16*60, 8*60, "Europe/Istanbul") // Fri 16:00-24:00 +03
	weekend := weekly(1<<int(time.Friday), 18*60, 63*60, "UTC")               // Fri 18:00 - Mon 09:00
	satNightWrap := weekly(1<<int(time.Saturday), 22*60, 4*60, "UTC")         // Sat 22:00 - Sun 02:00, across the week boundary
	dst := weekly(1<<int(time.Sunday), 1*60+30, 2*60, "Europe/Berlin")        // Sun 01:30 local for 2h
	for _, tc := range []struct {
		name string
		f    state.ReleaseFreeze
		now  string
		want bool
	}{
		{"window before start", state.ReleaseFreeze{Kind: state.ReleaseFreezeWindow, StartsAt: &start, EndsAt: &end}, "2026-12-23T23:59:59Z", false},
		{"window at start", state.ReleaseFreeze{Kind: state.ReleaseFreezeWindow, StartsAt: &start, EndsAt: &end}, "2026-12-24T00:00:00Z", true},
		{"window at end", state.ReleaseFreeze{Kind: state.ReleaseFreezeWindow, StartsAt: &start, EndsAt: &end}, "2026-12-27T00:00:00Z", false},
		{"open-ended window", state.ReleaseFreeze{Kind: state.ReleaseFreezeWindow, StartsAt: &start}, "2027-06-01T00:00:00Z", true},
		{"lifted window", state.ReleaseFreeze{Kind: state.ReleaseFreezeWindow, StartsAt: &start, LiftedAt: &lifted}, "2026-12-26T00:00:00Z", false},
		{"weekly inside, local time", friEvening, "2026-10-23T14:00:00Z", true},  // Fri 17:00 +03
		{"weekly before, local time", friEvening, "2026-10-23T12:59:59Z", false}, // Fri 15:59 +03
		{"weekly other day", friEvening, "2026-10-22T14:00:00Z", false},          // Thu
		{"weekend across midnight", weekend, "2026-10-25T12:00:00Z", true},       // Sun noon
		{"weekend ends Monday 09:00", weekend, "2026-10-26T09:00:00Z", false},
		{"Saturday night into Sunday", satNightWrap, "2026-10-25T01:00:00Z", true}, // Sun 01:00
		{"Saturday night after it ends", satNightWrap, "2026-10-25T02:00:00Z", false},
		{"weekly on DST change day", dst, "2026-10-25T00:45:00Z", true}, // 01:45 CEST, last Sunday of October
		{"after-rollback policy never active", state.ReleaseFreeze{Kind: state.ReleaseFreezeAfterRollback, AfterRollbackMinutes: 30}, "2026-10-25T00:00:00Z", false},
		{"weekly with an unknown zone", weekly(127, 0, 60, "Mars/Olympus"), "2026-10-25T00:30:00Z", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsActive(tc.f, mustTime(t, tc.now)); got != tc.want {
				t.Fatalf("IsActive at %s = %v, want %v", tc.now, got, tc.want)
			}
		})
	}
}

func TestActiveCoversAppAndAccountFreezes(t *testing.T) {
	now := mustTime(t, "2026-10-19T10:00:00Z")
	start := now.Add(-time.Hour)
	freezes := []state.ReleaseFreeze{
		{ID: "account", Kind: state.ReleaseFreezeWindow, StartsAt: &start},
		{ID: "this-app", AppID: "app-1", Kind: state.ReleaseFreezeWindow, StartsAt: &start},
		{ID: "other-app", AppID: "app-2", Kind: state.ReleaseFreezeWindow, StartsAt: &start},
	}
	got := Active(freezes, "app-1", now)
	if len(got) != 2 || got[0].ID != "account" || got[1].ID != "this-app" {
		t.Fatalf("Active = %+v", got)
	}
}

func TestNextStart(t *testing.T) {
	now := mustTime(t, "2026-10-21T10:00:00Z") // Wednesday
	soon := now.Add(48 * time.Hour)
	far := now.Add(8 * 24 * time.Hour)
	for _, tc := range []struct {
		name string
		f    state.ReleaseFreeze
		want string
	}{
		{"window within a week", state.ReleaseFreeze{Kind: state.ReleaseFreezeWindow, StartsAt: &soon}, "2026-10-23T10:00:00Z"},
		{"window beyond a week", state.ReleaseFreeze{Kind: state.ReleaseFreezeWindow, StartsAt: &far}, ""},
		{"weekly Friday evening", weekly(1<<int(time.Friday), 16*60, 60, "UTC"), "2026-10-23T16:00:00Z"},
		{"weekly later today", weekly(1<<int(time.Wednesday), 18*60, 60, "UTC"), "2026-10-21T18:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := NextStart(tc.f, now)
			if tc.want == "" {
				if ok {
					t.Fatalf("NextStart = %s, want none", got)
				}
				return
			}
			if !ok || !got.Equal(mustTime(t, tc.want)) {
				t.Fatalf("NextStart = %s (%v), want %s", got, ok, tc.want)
			}
		})
	}
}

func TestAfterRollbackPolicyTakesTheLongest(t *testing.T) {
	freezes := []state.ReleaseFreeze{
		{ID: "account", Kind: state.ReleaseFreezeAfterRollback, AfterRollbackMinutes: 30},
		{ID: "app", AppID: "app-1", Kind: state.ReleaseFreezeAfterRollback, AfterRollbackMinutes: 90},
		{ID: "other", AppID: "app-2", Kind: state.ReleaseFreezeAfterRollback, AfterRollbackMinutes: 600},
	}
	got, ok := AfterRollbackPolicy(freezes, "app-1")
	if !ok || got.ID != "app" {
		t.Fatalf("policy = %+v (%v)", got, ok)
	}
	if _, ok := AfterRollbackPolicy(freezes[2:], "app-1"); ok {
		t.Fatal("another app's policy applied")
	}
}

func TestFromRequest(t *testing.T) {
	now := mustTime(t, "2026-10-19T10:00:00Z")
	for _, tc := range []struct {
		name    string
		req     api.CreateReleaseFreezeRequest
		wantErr bool
		check   func(t *testing.T, f state.ReleaseFreeze)
	}{
		{name: "window defaults to now", req: api.CreateReleaseFreezeRequest{Kind: "window", EndsAt: "2026-10-19T12:00:00Z", Reason: "incident"},
			check: func(t *testing.T, f state.ReleaseFreeze) {
				if f.StartsAt == nil || !f.StartsAt.Equal(now) || f.EndsAt == nil {
					t.Fatalf("window = %+v", f)
				}
			}},
		{name: "window ending in the past", req: api.CreateReleaseFreezeRequest{Kind: "window", StartsAt: "2026-10-18T00:00:00Z", EndsAt: "2026-10-18T12:00:00Z", Reason: "old"}, wantErr: true},
		{name: "weekly", req: api.CreateReleaseFreezeRequest{Kind: "weekly", Reason: "Friday",
			Weekly: &api.ReleaseFreezeWeekly{Days: []string{"fri", "SAT"}, Start: "16:00", DurationMinutes: 480, TimeZone: "Europe/Istanbul"}},
			check: func(t *testing.T, f state.ReleaseFreeze) {
				if f.WeeklyDays != 1<<5|1<<6 || f.WeeklyStartMinute != 960 || f.WeeklyDurationMinutes != 480 {
					t.Fatalf("weekly = %+v", f)
				}
			}},
		{name: "weekly bad day", req: api.CreateReleaseFreezeRequest{Kind: "weekly", Reason: "x",
			Weekly: &api.ReleaseFreezeWeekly{Days: []string{"friday"}, Start: "16:00", DurationMinutes: 60, TimeZone: "UTC"}}, wantErr: true},
		{name: "weekly bad clock", req: api.CreateReleaseFreezeRequest{Kind: "weekly", Reason: "x",
			Weekly: &api.ReleaseFreezeWeekly{Days: []string{"fri"}, Start: "24:00", DurationMinutes: 60, TimeZone: "UTC"}}, wantErr: true},
		{name: "weekly longer than a week", req: api.CreateReleaseFreezeRequest{Kind: "weekly", Reason: "x",
			Weekly: &api.ReleaseFreezeWeekly{Days: []string{"fri"}, Start: "16:00", DurationMinutes: 10081, TimeZone: "UTC"}}, wantErr: true},
		{name: "weekly unknown zone", req: api.CreateReleaseFreezeRequest{Kind: "weekly", Reason: "x",
			Weekly: &api.ReleaseFreezeWeekly{Days: []string{"fri"}, Start: "16:00", DurationMinutes: 60, TimeZone: "Mars/Olympus"}}, wantErr: true},
		{name: "after rollback", req: api.CreateReleaseFreezeRequest{Kind: "after_rollback", AfterRollbackMinutes: 60, Reason: "settle"},
			check: func(t *testing.T, f state.ReleaseFreeze) {
				if f.AfterRollbackMinutes != 60 {
					t.Fatalf("policy = %+v", f)
				}
			}},
		{name: "after rollback too long", req: api.CreateReleaseFreezeRequest{Kind: "after_rollback", AfterRollbackMinutes: 1441, Reason: "x"}, wantErr: true},
		{name: "mixed fields", req: api.CreateReleaseFreezeRequest{Kind: "window", AfterRollbackMinutes: 5, Reason: "x"}, wantErr: true},
		{name: "missing reason", req: api.CreateReleaseFreezeRequest{Kind: "window"}, wantErr: true},
		{name: "unknown kind", req: api.CreateReleaseFreezeRequest{Kind: "forever", Reason: "x"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, p := FromRequest(tc.req, "acct", "", "user:test", now)
			if tc.wantErr != (p != nil) {
				t.Fatalf("problem = %v, wantErr %v", p, tc.wantErr)
			}
			if tc.check != nil {
				tc.check(t, f)
			}
		})
	}
}

func TestViewRendersWeeklyDays(t *testing.T) {
	now := mustTime(t, "2026-10-21T10:00:00Z")
	v := View(state.ReleaseFreeze{ID: "f", Kind: state.ReleaseFreezeWeekly, WeeklyDays: 1<<0 | 1<<5, WeeklyStartMinute: 16*60 + 30,
		WeeklyDurationMinutes: 90, TimeZone: "UTC", Reason: "r", CreatedAt: now}, "api", now)
	if v.Weekly == nil || len(v.Weekly.Days) != 2 || v.Weekly.Days[0] != "sun" || v.Weekly.Days[1] != "fri" || v.Weekly.Start != "16:30" {
		t.Fatalf("view = %+v", v.Weekly)
	}
	if v.Active || v.NextStartsAt != "2026-10-23T16:30:00Z" {
		t.Fatalf("active=%v next=%s", v.Active, v.NextStartsAt)
	}
}
