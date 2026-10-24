package conformance

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/publicstatus"
	"github.com/onebox-faas/faas/pkg/state"
)

// testIncidentNotesFollowTheirIncident pins ADR-20261022193512 slice 2's
// notes: one acknowledgement per account and incident, notes oldest first,
// account-scoped reads, the per-incident cap, an unknown incident not found,
// platform incidents noted per account, and notes going with a deleted rule.
func testIncidentNotesFollowTheirIncident(t *testing.T, fx *Fixture) {
	t.Helper()
	rule := createEpisodeRule(t, fx)
	t0 := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	if _, err := fx.Store.SetAlertRuleState(fx.Ctx, rule.ID, state.AlertStateFiring, t0); err != nil {
		t.Fatalf("SetAlertRuleState(firing): %v", err)
	}
	eps, err := fx.Store.ListAlertEpisodes(fx.Ctx, fx.Account.ID, fx.App.ID, t0.Add(-time.Hour), 1000)
	if err != nil {
		t.Fatalf("ListAlertEpisodes: %v", err)
	}
	var incidentID string
	for _, ep := range eps {
		if ep.RuleID == rule.ID {
			incidentID = "alert-" + ep.ID
		}
	}
	if incidentID == "" {
		t.Fatalf("no episode for rule %s in %+v", rule.ID, eps)
	}
	add := func(n state.IncidentNote, limit int) (state.IncidentNote, error) {
		t.Helper()
		n.AccountID = fx.Account.ID
		if n.IncidentID == "" {
			n.IncidentID = incidentID
		}
		return fx.Store.AddIncidentNote(fx.Ctx, n, limit)
	}
	ack, err := add(state.IncidentNote{Kind: state.IncidentNoteAcknowledged, Author: "ops@example.test", CreatedAt: t0.Add(time.Minute)}, 3)
	if err != nil || ack.ID == "" || ack.IncidentID != incidentID || !ack.CreatedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("acknowledge = %+v, %v", ack, err)
	}
	again, err := add(state.IncidentNote{Kind: state.IncidentNoteAcknowledged, Author: "api_key:k-2", CreatedAt: t0.Add(2 * time.Minute)}, 3)
	if err != nil || again.ID != ack.ID || again.Author != "ops@example.test" {
		t.Fatalf("second acknowledgement = %+v, %v; want the first", again, err)
	}
	// An upper-case uuid names the same incident.
	note, err := add(state.IncidentNote{IncidentID: "alert-" + strings.ToUpper(incidentID[len("alert-"):]), Kind: state.IncidentNoteText,
		Body: "rolled back to v41", Author: "ops@example.test", CreatedAt: t0.Add(3 * time.Minute)}, 3)
	if err != nil || note.IncidentID != incidentID || note.Body != "rolled back to v41" || note.Kind != state.IncidentNoteText {
		t.Fatalf("note = %+v, %v", note, err)
	}
	notes, err := fx.Store.ListIncidentNotes(fx.Ctx, fx.Account.ID, incidentID, 10)
	if err != nil || len(notes) != 2 || notes[0].ID != ack.ID || notes[1].ID != note.ID {
		t.Fatalf("ListIncidentNotes = %+v, %v; want the acknowledgement then the note", notes, err)
	}
	acks, err := fx.Store.IncidentAcknowledgements(fx.Ctx, fx.Account.ID, []string{incidentID, "alert-" + uuid.NewString()})
	if err != nil || len(acks) != 1 || acks[incidentID].ID != ack.ID {
		t.Fatalf("IncidentAcknowledgements = %+v, %v", acks, err)
	}
	other := uuid.NewString()
	if got, err := fx.Store.ListIncidentNotes(fx.Ctx, other, incidentID, 10); err != nil || len(got) != 0 {
		t.Fatalf("another account's notes = %+v, %v; want none", got, err)
	}
	if got, err := fx.Store.IncidentAcknowledgements(fx.Ctx, other, []string{incidentID}); err != nil || len(got) != 0 {
		t.Fatalf("another account's acknowledgements = %+v, %v; want none", got, err)
	}

	// The cap counts every note, the acknowledgement included; an existing
	// acknowledgement is still returned at the cap.
	if _, err := add(state.IncidentNote{Kind: state.IncidentNoteText, Body: "third", Author: "ops@example.test", CreatedAt: t0.Add(4 * time.Minute)}, 3); err != nil {
		t.Fatalf("third note: %v", err)
	}
	var quota *state.IncidentNotesQuotaError
	if _, err := add(state.IncidentNote{Kind: state.IncidentNoteText, Body: "fourth", Author: "ops@example.test"}, 3); !errors.As(err, &quota) || quota.Limit != 3 || quota.Observed != 3 {
		t.Fatalf("note past the cap = %v; want IncidentNotesQuotaError 3 of 3", err)
	}
	if got, err := add(state.IncidentNote{Kind: state.IncidentNoteAcknowledged, Author: "ops@example.test"}, 3); err != nil || got.ID != ack.ID {
		t.Fatalf("acknowledgement at the cap = %+v, %v; want the existing one", got, err)
	}
	for _, id := range []string{"alert-" + uuid.NewString(), "route-" + uuid.NewString(), "platform-" + uuid.NewString(), "alert-nope", "incident-" + uuid.NewString()} {
		if _, err := add(state.IncidentNote{IncidentID: id, Kind: state.IncidentNoteText, Body: "x", Author: "ops@example.test"}, 3); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("note on %q = %v, want ErrNotFound", id, err)
		}
	}

	// Platform incidents are shared; each account's notes on them are its own.
	ev, err := fx.Store.CreatePublicStatusEvent(fx.Ctx, state.StatusEventCreate{
		IdempotencyKey: "incident-notes-" + uuid.NewString(), Kind: publicstatus.KindIncident, Title: "Deploys delayed",
		Impact: publicstatus.StatePartialOutage, Components: []publicstatus.Component{publicstatus.ComponentDeployments},
		State: publicstatus.LifecycleIdentified, StartsAt: &t0, Message: "Builder queue backed up",
	})
	if err != nil {
		t.Fatalf("CreatePublicStatusEvent: %v", err)
	}
	platformID := "platform-" + ev.PublicID
	if _, err := add(state.IncidentNote{IncidentID: platformID, Kind: state.IncidentNoteAcknowledged, Author: "ops@example.test"}, 3); err != nil {
		t.Fatalf("acknowledge a platform incident: %v", err)
	}
	if got, err := fx.Store.IncidentAcknowledgements(fx.Ctx, other, []string{platformID}); err != nil || len(got) != 0 {
		t.Fatalf("another account's platform acknowledgements = %+v, %v; want none", got, err)
	}

	// Notes go with their incident.
	if err := fx.Store.DeleteAlertRule(fx.Ctx, rule.ID); err != nil {
		t.Fatalf("DeleteAlertRule: %v", err)
	}
	if got, err := fx.Store.ListIncidentNotes(fx.Ctx, fx.Account.ID, incidentID, 10); err != nil || len(got) != 0 {
		t.Fatalf("notes after the rule was deleted = %+v, %v; want none", got, err)
	}
	if got, err := fx.Store.IncidentAcknowledgements(fx.Ctx, fx.Account.ID, []string{incidentID, platformID}); err != nil || len(got) != 1 || got[platformID].ID == "" {
		t.Fatalf("acknowledgements after the rule was deleted = %+v, %v; want only the platform one", got, err)
	}
}
