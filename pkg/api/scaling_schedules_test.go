// adr: 195 — scheduled scaling floors.
package api

import (
	"strings"
	"testing"
)

func okSchedule() ScalingSchedule {
	return ScalingSchedule{Cron: "0 8 * * 1-5", DurationS: 12 * 3600, MinInstances: 3}
}

func TestValidateScalingSchedules_Accepts(t *testing.T) {
	if p := ValidateScalingSchedules("schedules", "Europe/Istanbul", []ScalingSchedule{okSchedule()}); p != nil {
		t.Fatalf("valid schedule rejected: %s", p.Detail)
	}
	// An empty timezone is UTC, not an error.
	if p := ValidateScalingSchedules("schedules", "", []ScalingSchedule{okSchedule()}); p != nil {
		t.Fatalf("empty timezone rejected: %s", p.Detail)
	}
	// No schedules at all is the shape every pre-ADR-195 app has.
	if p := ValidateScalingSchedules("schedules", "", nil); p != nil {
		t.Fatalf("empty list rejected: %s", p.Detail)
	}
}

func TestValidateScalingSchedules_Rejects(t *testing.T) {
	many := make([]ScalingSchedule, MaxScalingSchedules+1)
	for i := range many {
		many[i] = okSchedule()
	}
	cases := []struct {
		name     string
		timezone string
		in       []ScalingSchedule
		want     string
	}{
		{"bad cron", "UTC", []ScalingSchedule{{Cron: "every morning", DurationS: 3600, MinInstances: 1}}, "not a valid five-field cron"},
		{"missing cron", "UTC", []ScalingSchedule{{DurationS: 3600, MinInstances: 1}}, "cron is required"},
		{"bad timezone", "Mars/Olympus_Mons", []ScalingSchedule{okSchedule()}, "not a valid IANA zone"},
		{"duration too short", "UTC", []ScalingSchedule{{Cron: "0 8 * * *", DurationS: 30, MinInstances: 1}}, "duration_s must be between"},
		{"duration too long", "UTC", []ScalingSchedule{{Cron: "0 8 * * *", DurationS: MaxScalingScheduleDurationS + 1, MinInstances: 1}}, "duration_s must be between"},
		{"zero floor", "UTC", []ScalingSchedule{{Cron: "0 8 * * *", DurationS: 3600, MinInstances: 0}}, "must be > 0"},
		{"negative floor", "UTC", []ScalingSchedule{{Cron: "0 8 * * *", DurationS: 3600, MinInstances: -1}}, "must be > 0"},
		{"too many", "UTC", many, "at most"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := ValidateScalingSchedules("schedules", c.timezone, c.in)
			if p == nil {
				t.Fatalf("accepted, want rejection mentioning %q", c.want)
			}
			if !strings.Contains(p.Detail, c.want) {
				t.Errorf("detail = %q, want it to mention %q", p.Detail, c.want)
			}
		})
	}
}

// TestValidateScalingSchedules_TimezoneCheckedWithoutSchedules pins that a
// typo in the zone surfaces at write time rather than the first time a
// schedule is added to an app that already carries a bad zone.
func TestValidateScalingSchedules_TimezoneCheckedWithoutSchedules(t *testing.T) {
	if p := ValidateScalingSchedules("schedules", "Nowhere/Nothing", nil); p == nil {
		t.Fatal("an invalid timezone with no schedules was accepted")
	}
}

// TestMaxReachableMinInstances_GateInput is the plan-gate contract: a
// schedule buys the same warm capacity min_instances does, so a policy whose
// static floor is 0 must still report the schedule's floor to the gate.
func TestMaxReachableMinInstances_GateInput(t *testing.T) {
	p := &ScalingPolicy{MinInstances: 0, Schedules: []ScalingSchedule{okSchedule()}}
	if got := p.MaxReachableMinInstances(); got != 3 {
		t.Fatalf("MaxReachableMinInstances() = %d, want 3: a Free app could otherwise buy "+
			"a warm floor of 3 while declaring min_instances: 0", got)
	}
	if got := (*ScalingPolicy)(nil).MaxReachableMinInstances(); got != 0 {
		t.Errorf("nil policy = %d, want 0", got)
	}
}
