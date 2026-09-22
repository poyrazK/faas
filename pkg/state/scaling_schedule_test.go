// adr: 195 — scheduled scaling floors.
package state

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// ist is the zone used throughout: a UTC+3 offset with no DST, so a test
// that asserts "08:00 local" is not silently asserting "05:00 UTC in summer,
// 06:00 in winter".
const ist = "Europe/Istanbul"

func at(t *testing.T, iso string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		t.Fatalf("parse %q: %v", iso, err)
	}
	return parsed
}

// weekdayMornings keeps 3 instances warm for 12h from 08:00 Istanbul time,
// Monday to Friday.
func weekdayMornings() *ScalingPolicy {
	return &ScalingPolicy{
		Timezone: ist,
		Schedules: []ScalingSchedule{{
			Cron: "0 8 * * 1-5", DurationS: 12 * 3600, MinInstances: 3,
		}},
	}
}

func TestScheduledMinInstances_WindowBoundaries(t *testing.T) {
	p := weekdayMornings()
	// 2026-09-21 is a Monday. Istanbul is UTC+3, so 08:00 local = 05:00Z
	// and the window runs to 20:00 local = 17:00Z.
	cases := []struct {
		name string
		when string
		want int
	}{
		// One SECOND before the fire, not one minute: a sub-minute
		// boundary error hides in a coarser gap. A negative control that
		// shifted the comparison by 1s did not fail this table until
		// this case was tightened.
		{"one second before the window opens", "2026-09-21T04:59:59Z", 0},
		{"exactly at the fire", "2026-09-21T05:00:00Z", 3},
		{"mid window", "2026-09-21T11:00:00Z", 3},
		{"one second before close", "2026-09-21T16:59:59Z", 3},
		// Half-open: [fire, fire+duration). The instant the window
		// closes is outside it.
		{"exactly at close", "2026-09-21T17:00:00Z", 0},
		{"overnight", "2026-09-21T22:00:00Z", 0},
		// 2026-09-26 is a Saturday — the cron's 1-5 excludes it.
		{"weekend", "2026-09-26T11:00:00Z", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.ScheduledMinInstancesAt(at(t, c.when)); got != c.want {
				t.Errorf("ScheduledMinInstancesAt(%s) = %d, want %d", c.when, got, c.want)
			}
		})
	}
}

// TestScheduledMinInstances_TimezoneIsHonoured is the test that fails if the
// cron is ever evaluated in UTC. At 05:30Z the Istanbul window is open
// (08:30 local); a UTC evaluation would still be three hours from opening.
func TestScheduledMinInstances_TimezoneIsHonoured(t *testing.T) {
	p := weekdayMornings()
	if got := p.ScheduledMinInstancesAt(at(t, "2026-09-21T05:30:00Z")); got != 3 {
		t.Fatalf("08:30 Istanbul = %d, want 3: the cron was not evaluated in %s", got, ist)
	}
	utc := weekdayMornings()
	utc.Timezone = "UTC"
	if got := utc.ScheduledMinInstancesAt(at(t, "2026-09-21T05:30:00Z")); got != 0 {
		t.Fatalf("same instant in UTC = %d, want 0: the two zones must not agree here, "+
			"or this test cannot detect a zone that is ignored", got)
	}
}

// TestScheduledMinInstances_OverlappingWindowsTakeMax pins that order
// carries no meaning. A customer must not lose capacity by reordering a list.
func TestScheduledMinInstances_OverlappingWindowsTakeMax(t *testing.T) {
	low := ScalingSchedule{Cron: "0 8 * * *", DurationS: 12 * 3600, MinInstances: 2}
	high := ScalingSchedule{Cron: "0 9 * * *", DurationS: 2 * 3600, MinInstances: 7}
	when := at(t, "2026-09-21T07:00:00Z") // 10:00 Istanbul: both open.

	forward := &ScalingPolicy{Timezone: ist, Schedules: []ScalingSchedule{low, high}}
	reversed := &ScalingPolicy{Timezone: ist, Schedules: []ScalingSchedule{high, low}}
	if got := forward.ScheduledMinInstancesAt(when); got != 7 {
		t.Errorf("low-then-high = %d, want 7", got)
	}
	if got := reversed.ScheduledMinInstancesAt(when); got != 7 {
		t.Errorf("high-then-low = %d, want 7: schedule order must not change the floor", got)
	}
}

// TestEffectiveMinInstances_ScheduleOnlyRaises is the safety property. A
// window must never park an app that the static floor would keep warm.
func TestEffectiveMinInstances_ScheduleOnlyRaises(t *testing.T) {
	app := &App{
		MinInstances: 5,
		ScalingPolicy: &ScalingPolicy{
			Timezone: ist,
			// A schedule asking for LESS than the static floor.
			Schedules: []ScalingSchedule{{Cron: "0 8 * * *", DurationS: 3600, MinInstances: 1}},
		},
	}
	inside := app.EffectiveMinInstancesAt(at(t, "2026-09-21T05:30:00Z"))
	outside := app.EffectiveMinInstancesAt(at(t, "2026-09-21T22:00:00Z"))
	if inside != 5 || outside != 5 {
		t.Fatalf("inside=%d outside=%d, want 5 and 5: a schedule must never lower the floor", inside, outside)
	}
}

func TestEffectiveMinInstances_ScheduleRaisesAboveStatic(t *testing.T) {
	app := &App{ScalingPolicy: weekdayMornings()} // static floor 0
	if got := app.EffectiveMinInstancesAt(at(t, "2026-09-21T11:00:00Z")); got != 3 {
		t.Errorf("inside the window = %d, want 3", got)
	}
	if got := app.EffectiveMinInstancesAt(at(t, "2026-09-21T22:00:00Z")); got != 0 {
		t.Errorf("outside the window = %d, want 0: the app must park again", got)
	}
}

// TestScalingSchedule_MalformedRowIsInert is the defensive case. Validation
// rejects a bad cron at write time, but a row that somehow holds one must
// not read as permanently active — that would pin instances forever and bill
// for them.
func TestScalingSchedule_MalformedRowIsInert(t *testing.T) {
	for _, s := range []ScalingSchedule{
		{Cron: "not a cron", DurationS: 3600, MinInstances: 3},
		{Cron: "0 8 * * *", DurationS: 0, MinInstances: 3},
		{Cron: "0 8 * * *", DurationS: 3600, MinInstances: 0},
		{Cron: "", DurationS: 3600, MinInstances: 3},
	} {
		p := &ScalingPolicy{Timezone: ist, Schedules: []ScalingSchedule{s}}
		if got := p.ScheduledMinInstancesAt(at(t, "2026-09-21T11:00:00Z")); got != 0 {
			t.Errorf("%+v yielded floor %d, want 0", s, got)
		}
	}
	// An invalid TIMEZONE must be inert for the same reason.
	bad := weekdayMornings()
	bad.Timezone = "Mars/Olympus_Mons"
	if got := bad.ScheduledMinInstancesAt(at(t, "2026-09-21T11:00:00Z")); got != 0 {
		t.Errorf("invalid timezone yielded floor %d, want 0", got)
	}
}

// TestMaxReachableMinInstances is what every plan gate reads. A schedule buys
// the same warm capacity min_instances does, so the gate must see it.
func TestMaxReachableMinInstances(t *testing.T) {
	cases := []struct {
		name   string
		policy *ScalingPolicy
		want   int
	}{
		{"nil", nil, 0},
		{"static only", &ScalingPolicy{MinInstances: 2}, 2},
		{"schedule only", weekdayMornings(), 3},
		{"static higher", &ScalingPolicy{
			MinInstances: 9,
			Schedules:    []ScalingSchedule{{Cron: "0 8 * * *", DurationS: 3600, MinInstances: 3}},
		}, 9},
		{"schedule higher", &ScalingPolicy{
			MinInstances: 1,
			Schedules:    []ScalingSchedule{{Cron: "0 8 * * *", DurationS: 3600, MinInstances: 6}},
		}, 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.policy.MaxReachableMinInstances(); got != c.want {
				t.Errorf("MaxReachableMinInstances() = %d, want %d", got, c.want)
			}
		})
	}
	// The clock must not matter: the gate runs at PATCH time, which is
	// almost never inside the window being authorised.
	p := weekdayMornings()
	if p.MaxReachableMinInstances() != p.ScheduledMinInstancesAt(at(t, "2026-09-21T11:00:00Z")) {
		t.Error("reachable max disagreed with the in-window floor for a single-schedule policy")
	}
}

// TestScalingPolicy_SchedulesRoundTrip guards the jsonb encoding. The column
// was not migrated, so the marshal/unmarshal pair is the only thing carrying
// these fields to disk.
func TestScalingPolicy_SchedulesRoundTrip(t *testing.T) {
	in := weekdayMornings()
	blob, err := in.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out ScalingPolicy
	if err := out.UnmarshalJSON(blob); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Timezone != ist || len(out.Schedules) != 1 {
		t.Fatalf("round-trip lost fields: %+v (json %s)", out, blob)
	}
	if out.Schedules[0] != in.Schedules[0] {
		t.Errorf("schedule changed: %+v -> %+v", in.Schedules[0], out.Schedules[0])
	}
}

// TestScalingPolicy_EveryFieldSurvivesJSONRoundTrip is the regression for a
// bug ADR-195 uncovered in ADR-194's shipped code.
//
// UnmarshalJSON used to copy the decoded shape field by field into the
// policy. That list was hand-maintained, so it silently dropped every field
// added after it was written — `targets`, the entire ADR-194 multi-signal
// surface, was marshalled into apps.scaling_policy and thrown away on every
// read. The feature was inert in production while its whole test suite
// passed, because those tests built state.App in memory and never crossed
// this decoder.
//
// The fix was a struct conversion, which the compiler checks. This test is
// the behavioural half: it fills EVERY field with a distinctive non-zero
// value and requires the decoded policy to equal the encoded one, so a field
// added later without a decode path fails here rather than in production.
func TestScalingPolicy_EveryFieldSurvivesJSONRoundTrip(t *testing.T) {
	in := ScalingPolicy{
		MinInstances:            2,
		MaxInstances:            9,
		Target:                  &ScalingTarget{Metric: "rps", Value: 12.5},
		Targets:                 []ScalingTarget{{Metric: "cpu", Value: 70}, {Metric: "queue_depth", Value: 5}},
		ScaleOutCooldownS:       7,
		ScaleInCooldownS:        77,
		ConcurrencyOverflow:     "drop",
		MaxQueueWaitMS:          1234,
		WakeMaxQueueDepth:       31,
		WakeMaxQueueWaitSeconds: 41,
		Timezone:                ist,
		Schedules:               []ScalingSchedule{{Cron: "0 8 * * 1-5", DurationS: 43200, MinInstances: 3}},
	}
	blob, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out ScalingPolicy
	if err := json.Unmarshal(blob, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("a field did not survive the jsonb round-trip.\n in: %+v\nout: %+v\njson: %s\n\n"+
			"Every field of ScalingPolicy must appear in BOTH policyShape structs in types.go. "+
			"This is how ADR-194's `targets` shipped inert.", in, out, blob)
	}
}
