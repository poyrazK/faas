package api

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMCPReadinessAttentionValidation(t *testing.T) {
	version := int64(0)
	base := MCPReadinessAttentionAction{RequestID: uuid.NewString(), ExpectedVersion: &version, Action: "acknowledge", Reason: "Investigating"}
	for _, action := range []string{"acknowledge", "release", "configure_reminders"} {
		r := base
		r.Action = action
		if action == "configure_reminders" {
			r.ReminderPolicy = &MCPReadinessReminderPolicy{}
		}
		if err := ValidateMCPReadinessAttentionAction(r); err != nil {
			t.Fatal(r, err)
		}
	}
	for _, mutate := range []func(*MCPReadinessAttentionAction){
		func(r *MCPReadinessAttentionAction) { r.RequestID = "bad" }, func(r *MCPReadinessAttentionAction) { r.ExpectedVersion = nil }, func(r *MCPReadinessAttentionAction) { v := int64(-1); r.ExpectedVersion = &v }, func(r *MCPReadinessAttentionAction) { r.Reason = " " }, func(r *MCPReadinessAttentionAction) { r.Reason = strings.Repeat("é", 251) }, func(r *MCPReadinessAttentionAction) { r.Reason = "x\x00" }, func(r *MCPReadinessAttentionAction) { r.Reason = string([]byte{255}) }, func(r *MCPReadinessAttentionAction) { r.Action = "resolve" }, func(r *MCPReadinessAttentionAction) { r.Action = "release"; at := time.Now(); r.ExpiresAt = &at }, func(r *MCPReadinessAttentionAction) { r.Action = "configure_reminders" }, func(r *MCPReadinessAttentionAction) {
			r.ReminderPolicy = &MCPReadinessReminderPolicy{IntervalSeconds: 60, MaxReminders: 1}
		},
	} {
		r := base
		mutate(&r)
		if err := ValidateMCPReadinessAttentionAction(r); err == nil {
			t.Fatal("invalid request", r)
		}
	}
	for _, p := range []MCPReadinessReminderPolicy{{60, 1}, {86400, 10}, {0, 0}, {59, 1}, {86401, 1}, {60, 11}, {0, 1}, {60, 0}, {-1, -1}} {
		r := base
		r.Action = "configure_reminders"
		r.ReminderPolicy = &p
		valid := p.IntervalSeconds == 0 && p.MaxReminders == 0 || p.IntervalSeconds >= 60 && p.IntervalSeconds <= 86400 && p.MaxReminders >= 1 && p.MaxReminders <= 10
		if (ValidateMCPReadinessAttentionAction(r) == nil) != valid {
			t.Fatal(p)
		}
	}
}
