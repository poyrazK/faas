package faas

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	EventAcknowledgmentIntentCheckpointKind = "gregale.go.event-acknowledgment-intent-watch"
	EventAcknowledgmentIntentOutboxMax      = 1024
	EventAcknowledgmentIntentSequenceMax    = int64(9007199254740991)
)

var (
	ErrInvalidEventAcknowledgmentIntentCheckpoint = errors.New("invalid Go acknowledgment intent watch checkpoint")
	ErrInvalidEventAcknowledgmentIntentAlerts     = errors.New("invalid acknowledgment intent alert acknowledgments")
)

// EventAcknowledgmentIntentCheckpointPolicy binds every effective watch setting.
// Duration JSON numbers are nanoseconds, preserving Go's sub-millisecond settings.
type EventAcknowledgmentIntentCheckpointPolicy struct {
	ScanLimit        int           `json:"scan_limit"`
	StallAfter       time.Duration `json:"stall_after_ns"`
	DueThreshold     int64         `json:"due_threshold"`
	StalledThreshold int64         `json:"stalled_threshold"`
	PollInterval     time.Duration `json:"poll_interval_ns"`
	QueryTimeout     time.Duration `json:"query_timeout_ns"`
	MaxAge           time.Duration `json:"max_age_ns"`
	Sustain          time.Duration `json:"sustain_ns"`
}

type EventAcknowledgmentIntentCheckpointCondition struct {
	Metric     string     `json:"metric"`
	Threshold  int64      `json:"threshold"`
	State      string     `json:"state"` // unknown, clear, firing; historical after restart
	Value      *int64     `json:"value,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	IncidentID string     `json:"incident_id,omitempty"`
}

type EventAcknowledgmentIntentNotification struct {
	Sequence   int64     `json:"sequence"`
	IncidentID string    `json:"incident_id"`
	Metric     string    `json:"metric"`
	Transition string    `json:"transition"` // opened, resolved
	At         time.Time `json:"at"`
	Value      int64     `json:"value"`
	Threshold  int64     `json:"threshold"`
}

type EventAcknowledgmentIntentAcknowledgment struct {
	IncidentID string `json:"incident_id"`
	Sequence   int64  `json:"sequence"`
}

// EventAcknowledgmentIntentCheckpoint is application-persisted metadata. It
// contains no capabilities/payloads or unfinished monotonic confirmation proof.
// Validation cannot authenticate a snapshot or detect rollback to an older one.
type EventAcknowledgmentIntentCheckpoint struct {
	Kind           string                                         `json:"kind"`
	Version        int                                            `json:"version"`
	AccountID      string                                         `json:"account_id"`
	AppID          string                                         `json:"app_id"`
	Policy         EventAcknowledgmentIntentCheckpointPolicy      `json:"policy"`
	Session        string                                         `json:"session"`
	Sequence       int64                                          `json:"sequence"`
	LastObservedAt *time.Time                                     `json:"last_observed_at,omitempty"`
	Conditions     []EventAcknowledgmentIntentCheckpointCondition `json:"conditions"`
	Pending        []EventAcknowledgmentIntentNotification        `json:"pending"`
	Acknowledged   []EventAcknowledgmentIntentNotification        `json:"acknowledged"`
}

func eventIntentCheckpointPolicy(o EventAcknowledgmentIntentWatchOptions) EventAcknowledgmentIntentCheckpointPolicy {
	return EventAcknowledgmentIntentCheckpointPolicy{o.Health.ScanLimit, o.Health.StallAfter, o.DueThreshold, o.StalledThreshold, o.PollInterval, o.QueryTimeout, o.MaxAge, o.Sustain}
}

func (p EventAcknowledgmentIntentCheckpointPolicy) options() EventAcknowledgmentIntentWatchOptions {
	return EventAcknowledgmentIntentWatchOptions{
		Health:       EventAcknowledgmentIntentHealthOptions{ScanLimit: p.ScanLimit, StallAfter: p.StallAfter},
		DueThreshold: p.DueThreshold, StalledThreshold: p.StalledThreshold,
		PollInterval: p.PollInterval, QueryTimeout: p.QueryTimeout, MaxAge: p.MaxAge, Sustain: p.Sustain,
	}
}

func eventIntentCheckpointRules(p EventAcknowledgmentIntentCheckpointPolicy) []EventAcknowledgmentIntentCheckpointCondition {
	rules := []EventAcknowledgmentIntentCheckpointCondition{}
	if p.DueThreshold > 0 {
		rules = append(rules, EventAcknowledgmentIntentCheckpointCondition{Metric: "due_backlog", Threshold: p.DueThreshold, State: "unknown"})
	}
	if p.StalledThreshold > 0 {
		rules = append(rules, EventAcknowledgmentIntentCheckpointCondition{Metric: "stalled_recovery", Threshold: p.StalledThreshold, State: "unknown"})
	}
	return rules
}

func eventIntentCheckpointOpening(id, session string) (int64, bool) {
	raw, ok := strings.CutPrefix(id, session+"-")
	if !ok || len(raw) > 16 {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil && n > 0 && n <= EventAcknowledgmentIntentSequenceMax && strconv.FormatInt(n, 10) == raw
}

func eventIntentCheckpointTime(t time.Time) bool {
	return !t.IsZero() && t.UTC().Year() >= 1 && t.UTC().Year() <= 9999
}

func cloneEventIntentTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	out := t.UTC() // Strip process-local monotonic time and location state.
	return &out
}

func cloneEventIntentCheckpoint(c EventAcknowledgmentIntentCheckpoint) EventAcknowledgmentIntentCheckpoint {
	c.LastObservedAt = cloneEventIntentTime(c.LastObservedAt)
	c.Conditions = append([]EventAcknowledgmentIntentCheckpointCondition{}, c.Conditions...)
	for i := range c.Conditions {
		condition := &c.Conditions[i]
		condition.ObservedAt = cloneEventIntentTime(condition.ObservedAt)
		if condition.Value != nil {
			value := *condition.Value
			condition.Value = &value
		}
	}
	c.Pending = append([]EventAcknowledgmentIntentNotification{}, c.Pending...)
	c.Acknowledged = append([]EventAcknowledgmentIntentNotification{}, c.Acknowledged...)
	for _, rows := range [][]EventAcknowledgmentIntentNotification{c.Pending, c.Acknowledged} {
		for i := range rows {
			rows[i].At = rows[i].At.UTC()
		}
	}
	return c
}

// ValidateEventAcknowledgmentIntentCheckpoint returns a detached canonical copy.
// Decode bounded trusted JSON into the typed input first; unknown JSON fields are
// not retained. Bounds, policy, timestamps and incident/ack lineage are validated.
func ValidateEventAcknowledgmentIntentCheckpoint(saved EventAcknowledgmentIntentCheckpoint) (EventAcknowledgmentIntentCheckpoint, error) {
	var zero EventAcknowledgmentIntentCheckpoint
	if saved.Kind != EventAcknowledgmentIntentCheckpointKind || saved.Version != 1 || !operationUUID(saved.Session) || saved.Session != strings.ToLower(saved.Session) || saved.Sequence < 0 || saved.Sequence > EventAcknowledgmentIntentSequenceMax || len(saved.Pending) > EventAcknowledgmentIntentOutboxMax || len(saved.Acknowledged) > EventAcknowledgmentIntentOutboxMax {
		return zero, ErrInvalidEventAcknowledgmentIntentCheckpoint
	}
	account, app, err := eventIntentScope(saved.AccountID, saved.AppID)
	if err != nil {
		return zero, ErrInvalidEventAcknowledgmentIntentCheckpoint
	}
	options, err := eventIntentWatchOptions(saved.Policy.options())
	if err != nil || eventIntentCheckpointPolicy(options) != saved.Policy || len(saved.Conditions) != len(eventIntentCheckpointRules(saved.Policy)) {
		return zero, ErrInvalidEventAcknowledgmentIntentCheckpoint
	}
	if saved.LastObservedAt != nil && !eventIntentCheckpointTime(*saved.LastObservedAt) || saved.Sequence > 0 && saved.LastObservedAt == nil {
		return zero, ErrInvalidEventAcknowledgmentIntentCheckpoint
	}
	active := map[string]string{}
	for i, rule := range eventIntentCheckpointRules(saved.Policy) {
		c := saved.Conditions[i]
		if c.Metric != rule.Metric || c.Threshold != rule.Threshold || c.State != "unknown" && c.State != "clear" && c.State != "firing" || (c.Value == nil) != (c.ObservedAt == nil) {
			return zero, ErrInvalidEventAcknowledgmentIntentCheckpoint
		}
		if c.Value != nil && (*c.Value < 0 || *c.Value > int64(saved.Policy.ScanLimit) || !eventIntentCheckpointTime(*c.ObservedAt) || saved.LastObservedAt == nil || c.ObservedAt.After(*saved.LastObservedAt)) || saved.Sequence > 0 && c.Value == nil {
			return zero, ErrInvalidEventAcknowledgmentIntentCheckpoint
		}
		if c.IncidentID != "" {
			first, ok := eventIntentCheckpointOpening(c.IncidentID, saved.Session)
			if !ok || first > saved.Sequence || active[c.IncidentID] != "" || c.State == "clear" || c.Value == nil {
				return zero, ErrInvalidEventAcknowledgmentIntentCheckpoint
			}
			active[c.IncidentID] = c.Metric
		} else if c.State == "firing" {
			return zero, ErrInvalidEventAcknowledgmentIntentCheckpoint
		}
	}
	if len(saved.Pending)+len(active) > EventAcknowledgmentIntentOutboxMax || saved.Sequence > EventAcknowledgmentIntentSequenceMax-int64(len(active)) || !validEventIntentCheckpointNotifications(saved, active) {
		return zero, ErrInvalidEventAcknowledgmentIntentCheckpoint
	}
	out := cloneEventIntentCheckpoint(saved)
	out.AccountID, out.AppID = account, app
	return out, nil
}

type eventIntentNotificationLineage struct {
	metric                          string
	opened, resolved                int64
	openingPending, resolutionAcked bool
}

func validEventIntentCheckpointNotifications(c EventAcknowledgmentIntentCheckpoint, active map[string]string) bool {
	seen := map[int64]EventAcknowledgmentIntentNotification{}
	lineage := map[string]eventIntentNotificationLineage{}
	rules := map[string]int64{}
	for _, rule := range eventIntentCheckpointRules(c.Policy) {
		rules[rule.Metric] = rule.Threshold
	}
	for group, rows := range [][]EventAcknowledgmentIntentNotification{c.Pending, c.Acknowledged} {
		last := int64(0)
		for _, a := range rows {
			first, ok := eventIntentCheckpointOpening(a.IncidentID, c.Session)
			_, duplicate := seen[a.Sequence]
			threshold, metricOK := rules[a.Metric]
			if !ok || duplicate || !metricOK || a.Sequence <= last || a.Sequence > c.Sequence || first > a.Sequence || !eventIntentCheckpointTime(a.At) || a.Value < 0 || a.Value > int64(c.Policy.ScanLimit) || a.Threshold != threshold {
				return false
			}
			old := lineage[a.IncidentID]
			if old.metric != "" && old.metric != a.Metric {
				return false
			}
			old.metric = a.Metric
			switch a.Transition {
			case "opened":
				if first != a.Sequence || a.Value < threshold || old.opened != 0 {
					return false
				}
				old.opened, old.openingPending = a.Sequence, group == 0
			case "resolved":
				if first == a.Sequence || a.Value >= threshold || old.resolved != 0 {
					return false
				}
				old.resolved, old.resolutionAcked = a.Sequence, group == 1
			default:
				return false
			}
			lineage[a.IncidentID], seen[a.Sequence], last = old, a, a.Sequence
		}
	}
	for id, row := range lineage {
		metric, unresolved := active[id]
		if row.opened != 0 && row.resolved != 0 && row.resolved <= row.opened || row.resolved != 0 && unresolved || row.opened != 0 && row.resolved == 0 && !unresolved || unresolved && metric != row.metric || row.openingPending && row.resolutionAcked {
			return false
		}
	}
	for id := range active {
		if _, exists := lineage[id]; !exists {
			lineage[id] = eventIntentNotificationLineage{}
		}
	}
	for id := range lineage {
		first, _ := eventIntentCheckpointOpening(id, c.Session)
		if a, exists := seen[first]; exists && (a.IncidentID != id || a.Transition != "opened") {
			return false
		}
	}
	return true
}

// AcknowledgeEventAcknowledgmentIntentAlerts applies exact pairs all-or-nothing
// to a detached checkpoint. An opening must precede/accompany its resolution.
// Repeating an exact retained receipt is idempotent; history keeps the latest 1024.
func AcknowledgeEventAcknowledgmentIntentAlerts(saved EventAcknowledgmentIntentCheckpoint, acknowledgments []EventAcknowledgmentIntentAcknowledgment) (EventAcknowledgmentIntentCheckpoint, error) {
	out, err := ValidateEventAcknowledgmentIntentCheckpoint(saved)
	if err != nil {
		return EventAcknowledgmentIntentCheckpoint{}, err
	}
	if len(acknowledgments) > EventAcknowledgmentIntentOutboxMax {
		return EventAcknowledgmentIntentCheckpoint{}, ErrInvalidEventAcknowledgmentIntentAlerts
	}
	known := map[int64]EventAcknowledgmentIntentNotification{}
	for _, rows := range [][]EventAcknowledgmentIntentNotification{out.Pending, out.Acknowledged} {
		for _, a := range rows {
			known[a.Sequence] = a
		}
	}
	keys := map[int64]bool{}
	for _, ack := range acknowledgments {
		a, exists := known[ack.Sequence]
		if !exists || keys[ack.Sequence] || a.IncidentID != ack.IncidentID {
			return EventAcknowledgmentIntentCheckpoint{}, ErrInvalidEventAcknowledgmentIntentAlerts
		}
		keys[ack.Sequence] = true
	}
	openingPending := map[string]int64{}
	for _, a := range out.Pending {
		if a.Transition == "opened" {
			openingPending[a.IncidentID] = a.Sequence
		}
	}
	pending := []EventAcknowledgmentIntentNotification{}
	for _, a := range out.Pending {
		if keys[a.Sequence] {
			if first := openingPending[a.IncidentID]; a.Transition == "resolved" && first != 0 && !keys[first] {
				return EventAcknowledgmentIntentCheckpoint{}, ErrInvalidEventAcknowledgmentIntentAlerts
			}
			out.Acknowledged = append(out.Acknowledged, a)
		} else {
			pending = append(pending, a)
		}
	}
	sort.Slice(out.Acknowledged, func(i, j int) bool { return out.Acknowledged[i].Sequence < out.Acknowledged[j].Sequence })
	if len(out.Acknowledged) > EventAcknowledgmentIntentOutboxMax {
		out.Acknowledged = append([]EventAcknowledgmentIntentNotification{}, out.Acknowledged[len(out.Acknowledged)-EventAcknowledgmentIntentOutboxMax:]...)
	}
	out.Pending = pending
	return out, nil
}
