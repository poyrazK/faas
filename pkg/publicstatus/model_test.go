package publicstatus

import (
	"testing"
	"time"
)

func TestValidateEventRejectsInvalidPublicInput(t *testing.T) {
	now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		in   EventInput
		code string
	}{
		{
			name: "unknown component",
			in: EventInput{Kind: KindIncident, Title: "API errors", Impact: StateDegraded,
				Components: []Component{"database"}, State: LifecycleInvestigating, StartsAt: &now},
			code: CodeInvalidComponent,
		},
		{
			name: "title too long",
			in: EventInput{Kind: KindIncident, Title: string(make([]byte, 161)), Impact: StateDegraded,
				Components: []Component{ComponentAPIConsole}, State: LifecycleInvestigating, StartsAt: &now},
			code: CodeInvalidTitle,
		},
		{
			name: "incident cannot be scheduled",
			in: EventInput{Kind: KindIncident, Title: "API errors", Impact: StateDegraded,
				Components: []Component{ComponentAPIConsole}, State: LifecycleInvestigating,
				StartsAt: &now, ScheduledStartAt: &now},
			code: CodeInvalidSchedule,
		},
		{
			name: "maintenance end must follow start",
			in: EventInput{Kind: KindMaintenance, Title: "Network change", Impact: StateMaintenance,
				Components: []Component{ComponentNetworking}, State: LifecycleScheduled,
				ScheduledStartAt: &now, ScheduledEndAt: ptrTime(now.Add(-time.Minute))},
			code: CodeInvalidSchedule,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEvent(tt.in)
			if err == nil || err.Code != tt.code {
				t.Fatalf("ValidateEvent() error = %#v, want code %q", err, tt.code)
			}
		})
	}
}

func TestValidateTransitionPinsIncidentAndMaintenanceLifecycles(t *testing.T) {
	tests := []struct {
		name     string
		kind     Kind
		from     Lifecycle
		to       Lifecycle
		wantCode string
	}{
		{"incident advances", KindIncident, LifecycleInvestigating, LifecycleIdentified, ""},
		{"incident nonterminal moves backward", KindIncident, LifecycleMonitoring, LifecycleInvestigating, ""},
		{"incident resolves", KindIncident, LifecycleIdentified, LifecycleResolved, ""},
		{"resolved cannot reopen", KindIncident, LifecycleResolved, LifecycleMonitoring, CodeTerminalEvent},
		{"maintenance starts", KindMaintenance, LifecycleScheduled, LifecycleInProgress, ""},
		{"maintenance completes", KindMaintenance, LifecycleInProgress, LifecycleCompleted, ""},
		{"maintenance cancels", KindMaintenance, LifecycleScheduled, LifecycleCancelled, ""},
		{"maintenance cannot skip start", KindMaintenance, LifecycleScheduled, LifecycleCompleted, CodeInvalidTransition},
		{"completed is terminal", KindMaintenance, LifecycleCompleted, LifecycleInProgress, CodeTerminalEvent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTransition(tt.kind, tt.from, tt.to)
			if tt.wantCode == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || err.Code != tt.wantCode {
				t.Fatalf("error = %#v, want code %q", err, tt.wantCode)
			}
		})
	}
}

func TestValidateTransitionExhaustiveLifecycleMatrix(t *testing.T) {
	incidentStates := []Lifecycle{LifecycleInvestigating, LifecycleIdentified, LifecycleMonitoring, LifecycleResolved}
	for _, from := range incidentStates {
		for _, to := range incidentStates {
			allowed := incidentOpen(from) && (incidentOpen(to) || to == LifecycleResolved)
			err := ValidateTransition(KindIncident, from, to)
			if allowed && err != nil {
				t.Errorf("incident %s -> %s rejected: %v", from, to, err)
			}
			if !allowed && err == nil {
				t.Errorf("incident %s -> %s accepted", from, to)
			}
		}
	}

	maintenanceStates := []Lifecycle{LifecycleScheduled, LifecycleInProgress, LifecycleCompleted, LifecycleCancelled}
	for _, from := range maintenanceStates {
		for _, to := range maintenanceStates {
			allowed := (from == LifecycleScheduled && (to == LifecycleScheduled || to == LifecycleInProgress || to == LifecycleCancelled)) ||
				(from == LifecycleInProgress && (to == LifecycleInProgress || to == LifecycleCompleted))
			err := ValidateTransition(KindMaintenance, from, to)
			if allowed && err != nil {
				t.Errorf("maintenance %s -> %s rejected: %v", from, to, err)
			}
			if !allowed && err == nil {
				t.Errorf("maintenance %s -> %s accepted", from, to)
			}
		}
	}
}

func ptrTime(v time.Time) *time.Time { return &v }
