package apidserver_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261015173127929: read authorization and cursor bounds protect episode history.
func TestObsAlertTriageHistoryPagesAndEvidence(t *testing.T) {
	e := gapEnv(t)
	store := *(*e.S()).Store()
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second)
	episode := seedObsAlert(t, store, now, true)
	for i, action := range []string{"acknowledge", "claim", "release"} {
		if _, err := store.MutateOperatorAlertTriage(t.Context(), state.OperatorAlertTriageMutation{EpisodeID: episode.ID, ActorID: "11111111-1111-4111-8111-111111111111", Action: action, ExpectedRevision: int64(i), Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	base := "/v1/admin/obs/incidents/platform_alert/" + episode.ID
	rec := e.Do(t, "GET", base+"/triage-history?limit=1", nil, nil)
	var first api.ObsAlertTriageHistory
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(first.Items) != 1 || first.Items[0].Revision != 3 || first.NextCursor == "" || first.Coverage.State != "complete" {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}
	if _, err := store.MutateOperatorAlertTriage(t.Context(), state.OperatorAlertTriageMutation{EpisodeID: episode.ID, ActorID: "11111111-1111-4111-8111-111111111111", Action: "unacknowledge", ExpectedRevision: 3, Now: now}); err != nil {
		t.Fatal(err)
	}
	rec = e.Do(t, "GET", base+"/triage-history?limit=2&cursor="+url.QueryEscape(first.NextCursor), nil, nil)
	var second api.ObsAlertTriageHistory
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || second.SnapshotRevision != 3 || second.CurrentRevision != 4 || len(second.Items) != 2 || second.Items[0].Revision != 2 || second.Items[1].Revision != 1 || second.Truncated {
		t.Fatalf("next: %d %s", rec.Code, rec.Body)
	}
	rec = e.Do(t, "GET", base+"/evidence?limit=1", nil, nil)
	var evidence api.ObsIncidentEvidenceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &evidence); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || evidence.TriageHistory == nil || evidence.TriageHistory.SnapshotRevision != 4 || evidence.Incident.Triage == nil || evidence.Incident.Triage.Revision != 4 || !evidence.TriageHistory.Truncated || evidence.Incident.Status != "firing" {
		t.Fatalf("evidence: %d %s", rec.Code, rec.Body)
	}
	for _, suffix := range []string{"?limit=0", "?limit=101", "?cursor=bad"} {
		if rec := e.Do(t, "GET", base+"/triage-history"+suffix, nil, nil); rec.Code != 400 {
			t.Fatalf("bad query: %d %s", rec.Code, rec.Body)
		}
	}
	other := "/v1/admin/obs/incidents/platform_alert/" + strings.Repeat("f", 64) + "/triage-history"
	if rec := e.Do(t, "GET", other, nil, nil); rec.Code != 404 {
		t.Fatalf("not retained: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(string(mustHistoryJSON(t, first)), "owner_account_id") {
		t.Fatal("history exposed identity")
	}
}

func mustHistoryJSON(t *testing.T, h api.ObsAlertTriageHistory) []byte {
	t.Helper()
	body, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
