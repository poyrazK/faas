package incidents

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// noteRequestMaxBytes bounds a note request: IncidentNoteMaxChars runes of
// at most 12 bytes each (a JSON-escaped surrogate pair) and the envelope.
const noteRequestMaxBytes = 12*api.IncidentNoteMaxChars + 1024

// authorMaxRunes is incident_notes_author_check's bound.
const authorMaxRunes = 320

// Acknowledge serves POST /v1/incidents/{id}/acknowledge
// (ADR-20261022193512 slice 2): 201 with a new acknowledgement, 200 with the
// account's existing one, 409 for a resolved incident.
func (s *Service) Acknowledge(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.gate(w) {
		return
	}
	f, err := s.find(r.Context(), acct, r.PathValue("id"))
	if err != nil {
		s.writeReadErr(w, r, err)
		return
	}
	if f.incident.Status == api.IncidentStatusResolved {
		api.WriteProblem(w, api.ErrIncidentResolved())
		return
	}
	note, created, ok := s.addNote(w, r, acct, state.IncidentNote{IncidentID: f.incident.ID, Kind: state.IncidentNoteAcknowledged})
	if !ok {
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpkit.WriteJSON(w, status, noteDTO(note))
}

// AddNote serves POST /v1/incidents/{id}/notes, on open and resolved
// incidents alike.
func (s *Service) AddNote(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.gate(w) {
		return
	}
	var req api.IncidentNoteRequest
	if err := httpkit.DecodeJSONSized(r, &req, noteRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrIncidentsInvalid(`body must be {"body": "..."}`))
		return
	}
	body, prob := noteBody(req.Body)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	f, err := s.find(r.Context(), acct, r.PathValue("id"))
	if err != nil {
		s.writeReadErr(w, r, err)
		return
	}
	note, _, ok := s.addNote(w, r, acct, state.IncidentNote{IncidentID: f.incident.ID, Kind: state.IncidentNoteText, Body: body})
	if !ok {
		return
	}
	httpkit.WriteJSON(w, http.StatusCreated, noteDTO(note))
}

// noteBody trims a note and checks it against incident_notes_body_check.
func noteBody(raw string) (string, *api.Problem) {
	body := strings.TrimSpace(raw)
	switch {
	case body == "":
		return "", api.ErrIncidentsInvalid("a note needs a body")
	case utf8.RuneCountInString(body) > api.IncidentNoteMaxChars:
		return "", api.ErrIncidentsInvalid(fmt.Sprintf("a note is at most %d characters", api.IncidentNoteMaxChars))
	case strings.ContainsRune(body, 0):
		return "", api.ErrIncidentsInvalid("a note cannot contain NUL characters")
	}
	return body, nil
}

// addNote stores n for the caller and reports whether it is new; an
// acknowledgement that already existed comes back unchanged.
func (s *Service) addNote(w http.ResponseWriter, r *http.Request, acct state.Account, n state.IncidentNote) (state.IncidentNote, bool, bool) {
	author := s.Host.Actor(r, acct)
	if utf8.RuneCountInString(author) > authorMaxRunes {
		author = string([]rune(author)[:authorMaxRunes])
	}
	n.ID, n.AccountID, n.Author, n.CreatedAt = uuid.NewString(), acct.ID, author, s.now()
	saved, err := s.Host.Store().AddIncidentNote(r.Context(), n, api.IncidentNotesPerIncident)
	var quota *state.IncidentNotesQuotaError
	switch {
	case errors.As(err, &quota):
		api.WriteProblem(w, api.ErrIncidentNotesLimit(quota.Limit, quota.Observed))
		return state.IncidentNote{}, false, false
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.ErrIncidentNotFound())
		return state.IncidentNote{}, false, false
	case err != nil:
		s.Host.Logger().WarnContext(r.Context(), "incidents: add note", "kind", n.Kind, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not save the note"))
		return state.IncidentNote{}, false, false
	}
	return saved, saved.ID == n.ID, true
}

// addNotes attaches the account's notes, oldest first, and marks the
// incident acknowledged when one of them is the acknowledgement.
func (s *Service) addNotes(ctx context.Context, acct state.Account, detail *api.IncidentDetail) {
	notes, err := s.Host.Store().ListIncidentNotes(ctx, acct.ID, detail.Incident.ID, api.IncidentNotesPerIncident)
	if err != nil {
		s.Host.Logger().WarnContext(ctx, "incidents: read notes", "incident", detail.Incident.ID, "err", err)
		detail.UnavailableSources = append(detail.UnavailableSources, "notes")
		return
	}
	for _, n := range notes {
		if n.Kind == state.IncidentNoteAcknowledged {
			markAcknowledged(&detail.Incident, n)
		}
		detail.Notes = append(detail.Notes, noteDTO(n))
	}
}

func noteDTO(n state.IncidentNote) api.IncidentNote {
	return api.IncidentNote{ID: n.ID, Kind: string(n.Kind), Body: n.Body, Author: n.Author, CreatedAt: n.CreatedAt.UTC()}
}
