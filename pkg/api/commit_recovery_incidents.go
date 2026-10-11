package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CommitRecoveryBlockerIncident identifies a confirmed blocker episode in this watch.
type CommitRecoveryBlockerIncident struct {
	IncidentID     string                     `json:"incident_id"`
	Query          EventRecipientBlockerQuery `json:"query"`
	WaitingReason  string                     `json:"waiting_reason"`
	OpenedAt       time.Time                  `json:"opened_at"`
	LastObservedAt time.Time                  `json:"last_observed_at"`
}

// CommitRecoveryBlockerTransition is a bounded per-pass incident notification.
type CommitRecoveryBlockerTransition struct {
	Sequence      uint64                               `json:"sequence"`
	IncidentID    string                               `json:"incident_id"`
	Transition    string                               `json:"transition"`
	Reason        string                               `json:"reason"`
	At            time.Time                            `json:"at"`
	Query         EventRecipientBlockerQuery           `json:"query"`
	WaitingReason string                               `json:"waiting_reason"`
	Evidence      *CommitRecoveryDiagnosticObservation `json:"evidence,omitempty"`
}

type commitRecoveryActiveIncident struct {
	incident CommitRecoveryBlockerIncident
	first    time.Time
}

type commitRecoveryIncidentTracker struct {
	durable, queueFull bool
	pending            []CommitRecoveryBlockerTransition
	session            string
	sequence           uint64
	active             map[int]*commitRecoveryActiveIncident
	events             []CommitRecoveryBlockerTransition
}

func newCommitRecoveryIncidentTracker() *commitRecoveryIncidentTracker {
	var identity [16]byte
	_, _ = rand.Read(identity[:])
	return &commitRecoveryIncidentTracker{session: hex.EncodeToString(identity[:]), active: map[int]*commitRecoveryActiveIncident{}}
}

func (t *commitRecoveryIncidentTracker) beginPass() { t.events = nil }

func (t *commitRecoveryIncidentTracker) emit(incident CommitRecoveryBlockerIncident, transition, reason string, evidence *CommitRecoveryDiagnosticObservation) {
	t.sequence++
	event := CommitRecoveryBlockerTransition{Sequence: t.sequence, IncidentID: incident.IncidentID, Transition: transition, Reason: reason, At: time.Now().UTC(), Query: incident.Query, WaitingReason: incident.WaitingReason, Evidence: evidence}
	t.events = append(t.events, event)
	if t.durable {
		t.pending = append(t.pending, event)
	}
}

func (t *commitRecoveryIncidentTracker) summarize(out CommitRecoveryStatusResult, consumers []CommitRecoveryConsumerStall, age, threshold time.Duration) ([]CommitRecoveryBlockerIncident, []CommitRecoveryBlockerTransition) {
	if threshold == 0 {
		return nil, nil
	}
	waiting := map[string]CommitRecoveryPersistentBlocker{}
	for _, consumer := range consumers {
		for _, blocker := range consumer.Blockers {
			if blocker.State == "stalled" {
				waiting[incidentRecipientKey(blocker.Query)] = blocker
			}
		}
	}
	now := time.Now().UTC()
	seen := map[int]bool{}
	for i, row := range out.Items {
		if !diagnosticTarget(row) {
			continue
		}
		seen[i] = true
		query := EventRecipientBlockerQuery{Source: row.Selection.Publication.Source, ID: row.Selection.Publication.ID, SubscriptionID: strings.ToLower(row.Attempt.SubscriptionID), ExpectedAcceptedAt: row.Selection.Publication.AcceptedAt}
		blocker, stalled := waiting[incidentRecipientKey(query)]
		active := t.active[i]
		current := diagnosticObservation(row.Diagnostics, row)
		fresh := current != nil && diagnosticFreshness(row, now, age) == "fresh"
		if active != nil {
			uninterrupted := !active.incident.LastObservedAt.After(now) && now.Sub(active.incident.LastObservedAt) <= age
			sameEpisode := stalled && blocker.FirstObservedAt.Equal(active.first)
			if !fresh || !uninterrupted || !sameEpisode {
				transition, reason := "confirmation_lost", "episode_reset"
				if !fresh {
					reason = "evidence_unavailable"
				} else if !uninterrupted {
					reason = "sample_gap"
				} else if !row.Diagnostics.Retained && current.ObservedAt.After(active.incident.LastObservedAt) && (current.Status == "not_waiting" || (current.Routing != nil && !persistentRoutingBlocker(current.Routing.WaitingReason))) {
					transition, reason = "resolved", "routing_not_blocked"
				}
				var evidence *CommitRecoveryDiagnosticObservation
				if fresh {
					evidence = current
				}
				t.emit(active.incident, transition, reason, evidence)
				delete(t.active, i)
				active = nil
			}
		}
		if stalled && fresh {
			if active == nil {
				if !t.canOpen() {
					continue
				}
				incident := CommitRecoveryBlockerIncident{IncidentID: t.session + "-" + strconv.FormatUint(t.sequence+1, 10), Query: query, WaitingReason: blocker.WaitingReason, OpenedAt: now, LastObservedAt: current.ObservedAt}
				active = &commitRecoveryActiveIncident{incident: incident, first: blocker.FirstObservedAt}
				t.active[i] = active
				t.emit(incident, "opened", "persistent_blocker", current)
			}
			active.incident.LastObservedAt = current.ObservedAt
		}
	}
	missing := []int{}
	for i := range t.active {
		if !seen[i] {
			missing = append(missing, i)
		}
	}
	sort.Ints(missing)
	for _, i := range missing {
		t.emit(t.active[i].incident, "confirmation_lost", "selection_unavailable", nil)
		delete(t.active, i)
	}
	result := make([]CommitRecoveryBlockerIncident, 0, len(t.active))
	for i := range out.Items {
		if active := t.active[i]; active != nil {
			result = append(result, active.incident)
		}
	}
	events := append([]CommitRecoveryBlockerTransition{}, t.events...)
	return result, events
}

func incidentRecipientKey(query EventRecipientBlockerQuery) string {
	raw, _ := json.Marshal([]string{query.Source, query.ID, strings.ToLower(query.SubscriptionID), query.ExpectedAcceptedAt.UTC().Format(time.RFC3339Nano)})
	return string(raw)
}
