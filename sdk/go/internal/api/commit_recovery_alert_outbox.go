package api

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CommitRecoveryAlertAcknowledgement confirms processing of one exact transition.
type CommitRecoveryAlertAcknowledgement struct {
	IncidentID string `json:"incident_id"`
	Sequence   uint64 `json:"sequence"`
}

// CommitRecoveryAlertOutbox is a bounded checkpointable notification stream.
// Active incidents are saved evidence, never restored live confirmation.
type CommitRecoveryAlertOutbox struct {
	Version  int                               `json:"version"`
	Session  string                            `json:"session"`
	Sequence uint64                            `json:"sequence"`
	Pending  []CommitRecoveryBlockerTransition `json:"pending"`
	Active   []CommitRecoveryBlockerIncident   `json:"active"`
}

var commitRecoveryAlertSession = regexp.MustCompile(`^[0-9a-f]{32}$`)

func alertIncidentSequence(id, session string) (uint64, bool) {
	prefix := session + "-"
	if !strings.HasPrefix(id, prefix) {
		return 0, false
	}
	value := strings.TrimPrefix(id, prefix)
	n, err := strconv.ParseUint(value, 10, 64)
	return n, err == nil && n > 0 && strconv.FormatUint(n, 10) == value && n <= CommitRecoveryAlertSequenceMax
}

func validAlertTime(at time.Time) bool {
	return !at.IsZero() && (AppEventAcceptanceGuard{ExpectedAcceptedAt: &at}).Validate() == nil
}

func validAlertTransition(event CommitRecoveryBlockerTransition, session string, sequence uint64) bool {
	opened, ok := alertIncidentSequence(event.IncidentID, session)
	if !ok || opened > event.Sequence || event.Sequence > sequence || !validAlertTime(event.At) || !persistentRoutingBlocker(event.WaitingReason) {
		return false
	}
	if event.Evidence != nil && !validDiagnosticObservation(event.Evidence, event.Query) {
		return false
	}
	switch event.Transition {
	case "opened":
		return opened == event.Sequence && event.Reason == "persistent_blocker" && event.Evidence != nil && event.Evidence.Status == "available" && event.Evidence.Routing.WaitingReason == event.WaitingReason
	case "resolved":
		return event.Reason == "routing_not_blocked" && event.Evidence != nil && (event.Evidence.Status == "not_waiting" || !persistentRoutingBlocker(event.Evidence.Routing.WaitingReason))
	case "confirmation_lost":
		switch event.Reason {
		case "episode_reset", "evidence_unavailable", "sample_gap", "selection_unavailable", "watch_resumed":
			return true
		}
	}
	return false
}

func validateAlertOutbox(saved CommitRecoveryAlertOutbox, out CommitRecoveryStatusResult) error {
	if saved.Version != 1 || !commitRecoveryAlertSession.MatchString(saved.Session) || saved.Sequence > CommitRecoveryAlertSequenceMax || len(saved.Pending)+len(saved.Active) > CommitRecoveryAlertQueueMax || uint64(len(saved.Active)) > CommitRecoveryAlertSequenceMax-saved.Sequence {
		return ErrValidation("invalid saved alert outbox bounds")
	}
	targets := map[string]bool{}
	for _, row := range out.Items {
		if !diagnosticTarget(row) {
			continue
		}
		publication := row.Selection.Publication
		targets[incidentRecipientKey(EventRecipientBlockerQuery{Source: publication.Source, ID: publication.ID, SubscriptionID: strings.ToLower(row.Attempt.SubscriptionID), ExpectedAcceptedAt: publication.AcceptedAt})] = true
	}
	lineage, closed := map[string]string{}, map[string]bool{}
	var last uint64
	for _, event := range saved.Pending {
		if event.Sequence <= last || event.Query.Validate() != nil || !targets[incidentRecipientKey(event.Query)] || !validAlertTransition(event, saved.Session, saved.Sequence) {
			return ErrValidation("invalid saved pending alert or selection")
		}
		identity := incidentRecipientKey(event.Query) + "|" + event.WaitingReason
		if closed[event.IncidentID] || (lineage[event.IncidentID] != "" && lineage[event.IncidentID] != identity) {
			return ErrValidation("inconsistent saved alert episode")
		}
		lineage[event.IncidentID] = identity
		if event.Transition != "opened" {
			closed[event.IncidentID] = true
		}
		last = event.Sequence
	}
	identities, queries := map[string]bool{}, map[string]bool{}
	for _, incident := range saved.Active {
		opened, ok := alertIncidentSequence(incident.IncidentID, saved.Session)
		key := incidentRecipientKey(incident.Query)
		if !ok || opened > saved.Sequence || closed[incident.IncidentID] || (lineage[incident.IncidentID] != "" && lineage[incident.IncidentID] != key+"|"+incident.WaitingReason) || incident.Query.Validate() != nil || !targets[key] || identities[incident.IncidentID] || queries[key] || !validAlertTime(incident.OpenedAt) || !validAlertTime(incident.LastObservedAt) || !persistentRoutingBlocker(incident.WaitingReason) {
			return ErrValidation("invalid saved active alert")
		}
		identities[incident.IncidentID], queries[key] = true, true
	}
	return nil
}

func (t *commitRecoveryIncidentTracker) acknowledge(acks []CommitRecoveryAlertAcknowledgement) error {
	if len(acks) > CommitRecoveryAlertQueueMax || (!t.durable && len(acks) > 0) {
		return ErrValidation("alert acknowledgements require a bounded durable watch")
	}
	known := map[CommitRecoveryAlertAcknowledgement]bool{}
	pendingSequences := map[uint64]bool{}
	for _, event := range t.pending {
		known[CommitRecoveryAlertAcknowledgement{IncidentID: event.IncidentID, Sequence: event.Sequence}] = true
		pendingSequences[event.Sequence] = true
	}
	selected := map[CommitRecoveryAlertAcknowledgement]bool{}
	for _, ack := range acks {
		opened, validID := alertIncidentSequence(ack.IncidentID, t.session)
		alreadyRemoved := validID && opened <= ack.Sequence && ack.Sequence <= t.sequence && !pendingSequences[ack.Sequence]
		if selected[ack] || (!known[ack] && !alreadyRemoved) {
			return ErrValidation("acknowledgement does not match this alert stream")
		}

		selected[ack] = true
	}
	pending := make([]CommitRecoveryBlockerTransition, 0, len(t.pending))
	for _, event := range t.pending {
		if !selected[CommitRecoveryAlertAcknowledgement{IncidentID: event.IncidentID, Sequence: event.Sequence}] {
			pending = append(pending, event)
		}
	}
	t.pending = pending
	return nil
}

func (t *commitRecoveryIncidentTracker) restore(previous *CommitRecoveryStatusResult, out CommitRecoveryStatusResult, acks []CommitRecoveryAlertAcknowledgement) error {
	var saved *CommitRecoveryAlertOutbox
	if previous != nil && previous.Monitoring != nil {
		saved = previous.Monitoring.AlertOutbox
	}
	if saved == nil {
		return t.acknowledge(acks)
	}
	if !t.durable {
		return ErrValidation("resuming pending alert state requires durable alerts")
	}
	if !sameDiagnosticSelection(out, *previous) {
		return ErrValidation("saved alert selection mismatch")
	}
	if err := validateAlertOutbox(*saved, out); err != nil {
		return err
	}
	staged := &commitRecoveryIncidentTracker{durable: true, session: saved.Session, sequence: saved.Sequence, pending: append([]CommitRecoveryBlockerTransition{}, saved.Pending...)}
	if err := staged.acknowledge(acks); err != nil {
		return err
	}
	t.session, t.sequence, t.pending = saved.Session, saved.Sequence, staged.pending
	for _, incident := range saved.Active {
		t.emit(incident, "confirmation_lost", "watch_resumed", nil)
	}
	return nil
}

func (t *commitRecoveryIncidentTracker) outbox(active []CommitRecoveryBlockerIncident) *CommitRecoveryAlertOutbox {
	if !t.durable {
		return nil
	}
	return &CommitRecoveryAlertOutbox{Version: 1, Session: t.session, Sequence: t.sequence, Pending: append([]CommitRecoveryBlockerTransition{}, t.pending...), Active: append([]CommitRecoveryBlockerIncident{}, active...)}
}

func (t *commitRecoveryIncidentTracker) canOpen() bool {
	if !t.durable {
		return true
	}
	if t.queueFull {
		return false
	}
	if len(t.pending)+len(t.active)+2 > CommitRecoveryAlertQueueMax || t.sequence+uint64(len(t.active))+2 > CommitRecoveryAlertSequenceMax {
		t.queueFull = true
		return false
	}
	return true
}

// AcknowledgeCommitRecoveryAlerts validates a historical checkpoint queue and
// removes exact acknowledgements without polling or restoring live confirmation.
// The input is unchanged, including on validation failure.
func AcknowledgeCommitRecoveryAlerts(status CommitRecoveryStatusResult, acks []CommitRecoveryAlertAcknowledgement) (CommitRecoveryStatusResult, error) {
	if status.Monitoring == nil || status.Monitoring.AlertOutbox == nil {
		return status, ErrValidation("checkpoint has no durable alert outbox")
	}
	saved := status.Monitoring.AlertOutbox
	if err := validateAlertOutbox(*saved, status); err != nil {
		return status, err
	}
	staged := &commitRecoveryIncidentTracker{durable: true, session: saved.Session, sequence: saved.Sequence, pending: append([]CommitRecoveryBlockerTransition{}, saved.Pending...)}
	if err := staged.acknowledge(acks); err != nil {
		return status, err
	}
	monitoring := *status.Monitoring
	outbox := *saved
	outbox.Pending = staged.pending
	monitoring.AlertOutbox = &outbox
	status.Monitoring = &monitoring
	return status, nil
}

const CommitRecoveryAlertQueueMax = 1024
const CommitRecoveryAlertSequenceMax uint64 = 9007199254740991
