package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// IncidentNoteKind is what an incident note records.
type IncidentNoteKind string

const (
	// IncidentNoteAcknowledged says someone took the incident on; it has no
	// body, and an account acknowledges an incident at most once.
	IncidentNoteAcknowledged IncidentNoteKind = "acknowledged"
	IncidentNoteText         IncidentNoteKind = "note"
)

// IncidentNote is an account's note on a unified incident
// (ADR-20261022193512 slice 2). IncidentID is the API id: alert-, route- or
// platform- followed by the source row's uuid.
type IncidentNote struct {
	ID         string
	AccountID  string
	IncidentID string
	Kind       IncidentNoteKind
	Body       string
	Author     string
	CreatedAt  time.Time
}

// IncidentNoteStore keeps incident notes. Notes are append-only and go with
// their incident's source row.
type IncidentNoteStore interface {
	// AddIncidentNote appends a note, stamped now when CreatedAt is zero and
	// given a new id when ID is empty.
	// Acknowledging an incident the account already acknowledged returns
	// the existing acknowledgement unchanged. It returns ErrNotFound when
	// the incident's source row does not exist and *IncidentNotesQuotaError
	// when the account already holds limit notes on the incident.
	AddIncidentNote(ctx context.Context, n IncidentNote, limit int) (IncidentNote, error)
	// ListIncidentNotes returns the account's notes on the incident, oldest
	// first, at most limit.
	ListIncidentNotes(ctx context.Context, accountID, incidentID string, limit int) ([]IncidentNote, error)
	// IncidentAcknowledgements returns the account's acknowledgements of
	// the given incidents, keyed by incident id.
	IncidentAcknowledgements(ctx context.Context, accountID string, incidentIDs []string) (map[string]IncidentNote, error)
}

// IncidentNotesQuotaError reports the per-incident note cap.
type IncidentNotesQuotaError struct {
	Limit    int
	Observed int
}

func (e *IncidentNotesQuotaError) Error() string {
	return fmt.Sprintf("state: incident has %d of %d notes", e.Observed, e.Limit)
}

// incidentNoteSource splits an incident id into its source prefix and the
// source row's uuid, canonicalised so the id matches incident_notes'
// incident_notes_id_matches_source CHECK.
func incidentNoteSource(id string) (prefix, key string, ok bool) {
	for _, p := range []string{"alert-", "route-", "platform-"} {
		if rest, found := strings.CutPrefix(id, p); found {
			u, err := uuid.Parse(rest)
			if err != nil {
				return "", "", false
			}
			return p, u.String(), true
		}
	}
	return "", "", false
}

// prepareIncidentNote validates a note and canonicalises its incident id.
func prepareIncidentNote(n IncidentNote) (IncidentNote, string, string, error) {
	prefix, key, ok := incidentNoteSource(n.IncidentID)
	if !ok {
		return n, "", "", ErrNotFound
	}
	n.IncidentID = prefix + key
	if n.Kind != IncidentNoteAcknowledged && n.Kind != IncidentNoteText {
		return n, "", "", fmt.Errorf("state: incident note kind %q", n.Kind)
	}
	if (n.Kind == IncidentNoteText) != (n.Body != "") || n.Author == "" {
		return n, "", "", fmt.Errorf("state: incident note needs an author, and a body exactly when it is a note")
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	n.CreatedAt = n.CreatedAt.UTC().Truncate(time.Microsecond)
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	return n, prefix, key, nil
}
