package publicstatus

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type Component string

const (
	ComponentAPIConsole    Component = "api_console"
	ComponentDeployments   Component = "deployments"
	ComponentAppExecution  Component = "app_execution"
	ComponentNetworking    Component = "networking"
	ComponentObservability Component = "observability"
)

var publicComponents = []Component{
	ComponentAPIConsole,
	ComponentDeployments,
	ComponentAppExecution,
	ComponentNetworking,
	ComponentObservability,
}

func AllComponents() []Component {
	out := make([]Component, len(publicComponents))
	copy(out, publicComponents)
	return out
}

func ValidComponent(v Component) bool {
	for _, component := range publicComponents {
		if v == component {
			return true
		}
	}
	return false
}

type State string

const (
	StateOperational   State = "operational"
	StateMaintenance   State = "maintenance"
	StateDegraded      State = "degraded"
	StatePartialOutage State = "partial_outage"
	StateMajorOutage   State = "major_outage"
	StateUnknown       State = "unknown"
)

type Kind string

const (
	KindIncident    Kind = "incident"
	KindMaintenance Kind = "maintenance"
)

type Lifecycle string

const (
	LifecycleInvestigating Lifecycle = "investigating"
	LifecycleIdentified    Lifecycle = "identified"
	LifecycleMonitoring    Lifecycle = "monitoring"
	LifecycleResolved      Lifecycle = "resolved"
	LifecycleScheduled     Lifecycle = "scheduled"
	LifecycleInProgress    Lifecycle = "in_progress"
	LifecycleCompleted     Lifecycle = "completed"
	LifecycleCancelled     Lifecycle = "cancelled"
)

const (
	CodeInvalidComponent    = "status_invalid_component"
	CodeInvalidSchedule     = "status_invalid_schedule"
	CodeInvalidTransition   = "status_invalid_transition"
	CodeTerminalEvent       = "status_terminal_event"
	CodeInvalidTitle        = "status_invalid_title"
	CodeInvalidMessage      = "status_invalid_message"
	CodeInvalidEvent        = "status_invalid_event"
	CodeIdempotencyConflict = "status_idempotency_conflict"
)

type ValidationError struct {
	Code    string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

type EventInput struct {
	Kind             Kind
	Title            string
	Impact           State
	Components       []Component
	State            Lifecycle
	StartsAt         *time.Time
	ScheduledStartAt *time.Time
	ScheduledEndAt   *time.Time
}

func ValidateEvent(in EventInput) *ValidationError {
	if strings.TrimSpace(in.Title) == "" || utf8.RuneCountInString(in.Title) > 160 {
		return &ValidationError{Code: CodeInvalidTitle, Message: "title must contain 1 to 160 characters"}
	}
	if len(in.Components) == 0 {
		return &ValidationError{Code: CodeInvalidComponent, Message: "at least one affected component is required"}
	}
	seen := make(map[Component]struct{}, len(in.Components))
	for _, component := range in.Components {
		if !ValidComponent(component) {
			return &ValidationError{Code: CodeInvalidComponent, Message: fmt.Sprintf("unknown public component %q", component)}
		}
		if _, ok := seen[component]; ok {
			return &ValidationError{Code: CodeInvalidComponent, Message: fmt.Sprintf("duplicate public component %q", component)}
		}
		seen[component] = struct{}{}
	}

	switch in.Kind {
	case KindIncident:
		if in.State != LifecycleInvestigating && in.State != LifecycleIdentified && in.State != LifecycleMonitoring {
			return &ValidationError{Code: CodeInvalidEvent, Message: "a new incident must be investigating, identified, or monitoring"}
		}
		if in.Impact != StateDegraded && in.Impact != StatePartialOutage && in.Impact != StateMajorOutage {
			return &ValidationError{Code: CodeInvalidEvent, Message: "incident impact must be degraded, partial_outage, or major_outage"}
		}
		if in.StartsAt == nil || in.ScheduledStartAt != nil || in.ScheduledEndAt != nil {
			return &ValidationError{Code: CodeInvalidSchedule, Message: "incidents require starts_at and cannot have a maintenance schedule"}
		}
	case KindMaintenance:
		if in.State != LifecycleScheduled {
			return &ValidationError{Code: CodeInvalidEvent, Message: "new maintenance must be scheduled"}
		}
		if in.Impact != StateMaintenance {
			return &ValidationError{Code: CodeInvalidEvent, Message: "maintenance impact must be maintenance"}
		}
		if in.StartsAt != nil || in.ScheduledStartAt == nil || in.ScheduledEndAt == nil || !in.ScheduledEndAt.After(*in.ScheduledStartAt) {
			return &ValidationError{Code: CodeInvalidSchedule, Message: "maintenance requires scheduled_start_at before scheduled_end_at"}
		}
	default:
		return &ValidationError{Code: CodeInvalidEvent, Message: "kind must be incident or maintenance"}
	}
	return nil
}

func ValidateMessage(message string) *ValidationError {
	if strings.TrimSpace(message) == "" || utf8.RuneCountInString(message) > 1024 {
		return &ValidationError{Code: CodeInvalidMessage, Message: "message must contain 1 to 1024 characters"}
	}
	return nil
}

func ValidateTransition(kind Kind, from, to Lifecycle) *ValidationError {
	if from == to {
		if terminal(from) {
			return &ValidationError{Code: CodeTerminalEvent, Message: "terminal events cannot be updated"}
		}
		return nil
	}
	if terminal(from) {
		return &ValidationError{Code: CodeTerminalEvent, Message: "terminal events cannot be reopened"}
	}
	switch kind {
	case KindIncident:
		if incidentOpen(from) && (incidentOpen(to) || to == LifecycleResolved) {
			return nil
		}
	case KindMaintenance:
		if from == LifecycleScheduled && (to == LifecycleInProgress || to == LifecycleCancelled) {
			return nil
		}
		if from == LifecycleInProgress && to == LifecycleCompleted {
			return nil
		}
	}
	return &ValidationError{Code: CodeInvalidTransition, Message: fmt.Sprintf("cannot move %s from %s to %s", kind, from, to)}
}

func incidentOpen(v Lifecycle) bool {
	return v == LifecycleInvestigating || v == LifecycleIdentified || v == LifecycleMonitoring
}

func terminal(v Lifecycle) bool {
	return v == LifecycleResolved || v == LifecycleCompleted || v == LifecycleCancelled
}
