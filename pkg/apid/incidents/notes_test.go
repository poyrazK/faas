package incidents

// adr: 20261022193512 — slice 2: an account acknowledges an open incident
// once, adds append-only notes to open and resolved ones, and sees them on
// the list and the detail.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/publicstatus"
	"github.com/onebox-faas/faas/pkg/state"
)

func post(t *testing.T, h func(http.ResponseWriter, *http.Request, state.Account), acct state.Account, id, actor, body string, out any) (int, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/incidents/"+id+"/x", strings.NewReader(body))
	r.SetPathValue("id", id)
	r.Header.Set("X-Test-Actor", actor)
	w := httptest.NewRecorder()
	h(w, r, acct)
	if out != nil && w.Code < 300 {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v", w.Body.String(), err)
		}
	}
	return w.Code, w.Body.String()
}

func (f fixture) openEpisode(t *testing.T) string {
	t.Helper()
	f.setState(t, state.AlertStateFiring, f.now.Add(-20*time.Minute))
	eps, err := f.store.ListAlertEpisodes(context.Background(), f.acct.ID, "", f.now.Add(-time.Hour), 10)
	if err != nil || len(eps) != 1 {
		t.Fatalf("episodes = %+v, %v", eps, err)
	}
	return alertPrefix + eps[0].ID
}

func TestAcknowledgeOnceThenNotes(t *testing.T) {
	f := newFixture(t)
	id := f.openEpisode(t)
	var ack api.IncidentNote
	if code, body := post(t, f.svc.Acknowledge, f.acct, id, "ops@example.com", "", &ack); code != http.StatusCreated ||
		ack.Kind != api.IncidentNoteKindAcknowledged || ack.Author != "ops@example.com" || !ack.CreatedAt.Equal(f.now) {
		t.Fatalf("acknowledge = %d %s", code, body)
	}
	var again api.IncidentNote
	if code, body := post(t, f.svc.Acknowledge, f.acct, id, "api_key:k-2", "", &again); code != http.StatusOK || again.ID != ack.ID || again.Author != "ops@example.com" {
		t.Fatalf("second acknowledge = %d %s; want 200 with the first", code, body)
	}
	f.svc.Now = func() time.Time { return f.now.Add(time.Minute) }
	var note api.IncidentNote
	if code, body := post(t, f.svc.AddNote, f.acct, id, "api_key:k-2", `{"body": "  rolled back to v41\n"}`, &note); code != http.StatusCreated ||
		note.Body != "rolled back to v41" || note.Kind != api.IncidentNoteKindNote || note.Author != "api_key:k-2" {
		t.Fatalf("note = %d %s", code, body)
	}

	var detail api.IncidentDetail
	if code := get(t, f.svc.Get, f.acct, "/v1/incidents/x", id, &detail); code != http.StatusOK {
		t.Fatalf("detail = %d", code)
	}
	if len(detail.Notes) != 2 || detail.Notes[0].ID != ack.ID || detail.Notes[1].ID != note.ID ||
		detail.Incident.AcknowledgedBy != "ops@example.com" || detail.Incident.AcknowledgedAt == nil {
		t.Fatalf("detail notes = %+v, incident = %+v", detail.Notes, detail.Incident)
	}
	var list api.IncidentsResponse
	if code := get(t, f.svc.List, f.acct, "/v1/incidents", "", &list); code != http.StatusOK || len(list.Incidents) != 1 ||
		list.Incidents[0].AcknowledgedBy != "ops@example.com" || !list.Incidents[0].AcknowledgedAt.Equal(f.now) {
		t.Fatalf("list = %d %+v", code, list)
	}

	// A resolved incident takes notes but not an acknowledgement.
	f.setState(t, state.AlertStateOk, f.now)
	if code, body := post(t, f.svc.Acknowledge, f.acct, id, "ops@example.com", "", nil); code != http.StatusConflict || !strings.Contains(body, "incident_resolved") {
		t.Fatalf("acknowledge resolved = %d %s; want 409 incident_resolved", code, body)
	}
	if code, body := post(t, f.svc.AddNote, f.acct, id, "ops@example.com", `{"body": "postmortem: PM-12"}`, nil); code != http.StatusCreated {
		t.Fatalf("note on a resolved incident = %d %s", code, body)
	}
}

func TestNoteValidationAndScope(t *testing.T) {
	f := newFixture(t)
	id := f.openEpisode(t)
	for name, body := range map[string]string{
		"not json":   `rolled back`,
		"empty":      `{"body": "   "}`,
		"too long":   `{"body": "` + strings.Repeat("é", api.IncidentNoteMaxChars+1) + `"}`,
		"nul":        `{"body": "a\u0000b"}`,
		"wrong type": `{"body": 3}`,
	} {
		if code, resp := post(t, f.svc.AddNote, f.acct, id, "ops@example.com", body, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s; want 400", name, code, resp)
		}
	}
	if code, _ := post(t, f.svc.AddNote, f.acct, id, "ops@example.com", `{"body": "`+strings.Repeat("é", api.IncidentNoteMaxChars)+`"}`, nil); code != http.StatusCreated {
		t.Errorf("a note of exactly %d characters = %d; want 201", api.IncidentNoteMaxChars, code)
	}
	other, err := f.store.CreateAccount(context.Background(), "other-incidents@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		acct state.Account
		id   string
	}{{other, id}, {f.acct, "alert-not-a-uuid"}, {f.acct, "nope"}} {
		if code, _ := post(t, f.svc.Acknowledge, target.acct, target.id, "ops@example.com", "", nil); code != http.StatusNotFound {
			t.Errorf("acknowledge %s as %s = %d; want 404", target.id, target.acct.Email, code)
		}
		if code, _ := post(t, f.svc.AddNote, target.acct, target.id, "ops@example.com", `{"body": "x"}`, nil); code != http.StatusNotFound {
			t.Errorf("note %s as %s = %d; want 404", target.id, target.acct.Email, code)
		}
	}
	f.svc.Enabled = false
	if code, _ := post(t, f.svc.Acknowledge, f.acct, id, "ops@example.com", "", nil); code != http.StatusServiceUnavailable {
		t.Errorf("acknowledge with the gate off = %d; want 503", code)
	}
}

func TestNotesCapAndPlatformIncidentsPerAccount(t *testing.T) {
	f := newFixture(t)
	id := f.openEpisode(t)
	for i := range api.IncidentNotesPerIncident {
		if code, body := post(t, f.svc.AddNote, f.acct, id, "ops@example.com", `{"body": "n"}`, nil); code != http.StatusCreated {
			t.Fatalf("note %d = %d %s", i, code, body)
		}
	}
	code, body := post(t, f.svc.AddNote, f.acct, id, "ops@example.com", `{"body": "one more"}`, nil)
	if code != http.StatusConflict || !strings.Contains(body, "incident_notes_limit") || !strings.Contains(body, `"limit":100`) {
		t.Fatalf("note past the cap = %d %s; want 409 incident_notes_limit with the limit", code, body)
	}

	started := f.now.Add(-time.Hour)
	ev, err := f.store.CreatePublicStatusEvent(context.Background(), state.StatusEventCreate{
		IdempotencyKey: "notes-platform", Kind: publicstatus.KindIncident, Title: "Deploys delayed",
		Impact: publicstatus.StatePartialOutage, Components: []publicstatus.Component{publicstatus.ComponentDeployments},
		State: publicstatus.LifecycleIdentified, StartsAt: &started, Message: "Builder queue backed up",
	})
	if err != nil {
		t.Fatal(err)
	}
	platform := platformPrefix + ev.PublicID
	if code, body := post(t, f.svc.Acknowledge, f.acct, platform, "ops@example.com", "", nil); code != http.StatusCreated {
		t.Fatalf("acknowledge a platform incident = %d %s", code, body)
	}
	other, err := f.store.CreateAccount(context.Background(), "other-platform@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	var detail api.IncidentDetail
	if code := get(t, f.svc.Get, other, "/v1/incidents/x", platform, &detail); code != http.StatusOK || len(detail.Notes) != 0 || detail.Incident.AcknowledgedAt != nil {
		t.Fatalf("another account's view = %d notes %+v ack %v; want its own (none)", code, detail.Notes, detail.Incident.AcknowledgedAt)
	}
}
