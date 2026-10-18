package api

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestMCPReadinessReminderDefaultsValidation(t *testing.T) {
	zero := int64(0)
	max := int64(math.MaxInt64)
	negative := int64(-1)
	off := MCPReadinessReminderPolicy{}
	on := MCPReadinessReminderPolicy{IntervalSeconds: 60, MaxReminders: 1}
	for _, r := range []SetMCPReadinessReminderDefaultsRequest{{ExpectedVersion: &zero, ReminderPolicy: &off}, {ExpectedVersion: &zero, ReminderPolicy: &on}} {
		if e := ValidateMCPReadinessReminderDefaultsRequest(r); e != nil {
			t.Fatal(e)
		}
	}
	for _, r := range []SetMCPReadinessReminderDefaultsRequest{{}, {ExpectedVersion: &zero}, {ReminderPolicy: &off}, {ExpectedVersion: &max, ReminderPolicy: &off}, {ExpectedVersion: &negative, ReminderPolicy: &off}} {
		if ValidateMCPReadinessReminderDefaultsRequest(r) == nil {
			t.Fatal("invalid", r)
		}
	}
	for _, p := range []MCPReadinessReminderPolicy{{IntervalSeconds: 59, MaxReminders: 1}, {IntervalSeconds: 86401, MaxReminders: 1}, {IntervalSeconds: 60, MaxReminders: 11}, {IntervalSeconds: 60}, {MaxReminders: 1}, {IntervalSeconds: -1}} {
		if ValidateMCPReadinessReminderPolicy(p) == nil {
			t.Fatal(p)
		}
	}
	if e := ValidateMCPReadinessReminderPolicy(MCPReadinessReminderPolicy{IntervalSeconds: 86400, MaxReminders: 10}); e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(MCPReadinessIncidentAttention{ReminderPolicySource: "rule_default", ReminderDefaultsVersion: &zero})
	if e != nil || !strings.Contains(string(b), `"reminder_defaults_version":0`) {
		t.Fatal(string(b), e)
	}
}
