package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func seedObsAlert(t *testing.T, store state.Store, at time.Time, active bool) state.OperatorAlertEpisode {
	t.Helper()
	key, series := strings.Repeat("a", 64), strings.Repeat("b", 64)
	e := state.OperatorAlertEpisode{RuleKey: key, SeriesID: series, Name: "FaasApiDown", Component: "apid", Severity: "critical", StartedAt: at.Add(-time.Minute), RunbookURL: "/docs/runbooks/FaasApiDown.md"}
	e.ID = state.OperatorAlertEpisodeID(key, series, e.StartedAt)
	s := state.OperatorAlertSnapshot{ObservedAt: at, Rules: []state.OperatorAlertRuleObservation{{Key: key, EvaluatedAt: at, EpisodeIDs: []string{}}}, Episodes: []state.OperatorAlertEpisode{}}
	if active {
		s.Rules[0].EpisodeIDs = append(s.Rules[0].EpisodeIDs, e.ID)
		s.Episodes = append(s.Episodes, e)
	}
	if err := store.RecordOperatorAlertSnapshot(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestObsRetainedAlertsRecoverAndRemainReadableOffline(t *testing.T) {
	e := gapEnv(t)
	store := *(*e.S()).Store()
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-5 * time.Second)
	first := seedObsAlert(t, store, now, true)
	seedObsAlert(t, store, now.Add(time.Second), false)
	seedObsAlert(t, store, now.Add(2*time.Second), true)
	if err := store.RecordOperatorAlertFailure(context.Background(), now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	// There is no Prometheus client in this environment. Retained evidence and
	// both historical/current-unknown episodes must still return 200.
	rec := e.Do(t, "GET", "/v1/admin/obs/incidents?type=platform_alert&limit=1", nil, nil)
	var list api.ObsIncidentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(list.Items) != 1 || list.NextCursor == "" || len(list.UnavailableSources) != 1 || list.UnavailableSources[0] != "platform_alert_history" {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	next := e.Do(t, "GET", "/v1/admin/obs/incidents?type=platform_alert&limit=1&cursor="+list.NextCursor, nil, nil)
	var second api.ObsIncidentListResponse
	if err := json.Unmarshal(next.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if next.Code != 200 || len(second.Items) != 1 || second.Items[0].ID == list.Items[0].ID {
		t.Fatalf("second page status=%d body=%s", next.Code, next.Body.String())
	}
	path := "/v1/admin/obs/incidents/platform_alert/" + first.ID + "/evidence?limit=1"
	rec = e.Do(t, "GET", path, nil, nil)
	var evidence api.ObsIncidentEvidenceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &evidence); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || evidence.Incident.Status != "resolved" || !evidence.Truncated || evidence.Incident.AlertEpisode == nil || evidence.Incident.AlertEpisode.ResolvedAt == nil || evidence.History[0].Kind != "platform_alert.resolved" {
		t.Fatalf("evidence status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = e.Do(t, "GET", "/v1/admin/obs/incidents?type=platform_alert&alert_status=resolved", nil, nil)
	list = api.ObsIncidentListResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || len(list.Items) != 1 || list.Items[0].ID != "platform_alert/"+first.ID {
		t.Fatalf("status filter %d %s", rec.Code, rec.Body.String())
	}
	rec = e.Do(t, "GET", "/v1/admin/obs/incidents/platform_alert/"+first.SeriesID+"/evidence", nil, nil)
	evidence = api.ObsIncidentEvidenceResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &evidence); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || evidence.Incident.Status != "unknown" || evidence.Incident.ID == "platform_alert/"+first.ID {
		t.Fatalf("legacy latest link %d %s", rec.Code, rec.Body.String())
	}
	rec = e.Do(t, "GET", "/v1/admin/obs/incidents?type=platform_alert&since="+now.Add(-20*24*time.Hour).Format(time.RFC3339), nil, nil)
	list = api.ObsIncidentListResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || list.SinceClamped {
		t.Fatalf("retained history window %d %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/v1/admin/obs/incidents?alert_status=resolved", "/v1/admin/obs/incidents?type=deployment&alert_status=resolved", "/v1/admin/obs/incidents?type=platform_alert&alert_status=healthy"} {
		rec = e.Do(t, "GET", path, nil, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid filter %s: %d", path, rec.Code)
		}
	}
	rec = e.Do(t, "GET", "/v1/admin/obs/health", nil, nil)
	var health api.ObsHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || health.AlertHistory == nil || health.AlertHistory.State != "unknown" || health.AlertHistory.Reason != "collection_failed" {
		t.Fatalf("history health status=%d body=%s", rec.Code, rec.Body.String())
	}
}

type obsAlertFailureStore struct{ Store state.Store }

func (s *obsAlertFailureStore) OperatorAlertEpisodes(context.Context, state.OperatorAlertFilter) (state.OperatorAlertPage, error) {
	return state.OperatorAlertPage{}, errors.New("private database detail")
}
func (s *obsAlertFailureStore) OperatorAlertEpisode(context.Context, string) (state.OperatorAlertEpisode, state.OperatorAlertCollection, error) {
	return state.OperatorAlertEpisode{}, state.OperatorAlertCollection{}, errors.New("private database detail")
}
func (s *obsAlertFailureStore) OperatorAlertTriageHistory(context.Context, state.OperatorAlertTriageHistoryFilter) (state.OperatorAlertTriageHistorySnapshot, error) {
	return state.OperatorAlertTriageHistorySnapshot{}, errors.New("private database detail")
}
func (s *obsAlertFailureStore) OperatorAlertCollectionStatus(context.Context) (state.OperatorAlertCollection, error) {
	return state.OperatorAlertCollection{}, errors.New("private database detail")
}

func TestObsAlertHistoryDatabaseFailureIsUnavailable(t *testing.T) {
	e := gapEnv(t)
	fake := &obsAlertFailureStore{Store: *(*e.S()).Store()}
	*(*e.S()).Store() = statetest.NewStore(fake)
	for _, path := range []string{"/v1/admin/obs/incidents?type=platform_alert", "/v1/admin/obs/incidents/platform_alert/" + strings.Repeat("a", 64) + "/evidence", "/v1/admin/obs/health", "/v1/admin/obs/incidents/platform_alert/" + strings.Repeat("a", 64) + "/triage-history"} {
		rec := e.Do(t, "GET", path, nil, nil)
		if rec.Code != 503 || strings.Contains(rec.Body.String(), "private database detail") {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestObsAlertHistoryRemainsAdminOnly(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesReadSurface, "ops@faas.dev", "customer@faas.dev")
	for _, path := range []string{"/v1/admin/obs/incidents?type=platform_alert&alert_status=resolved", "/v1/admin/obs/incidents/platform_alert/" + strings.Repeat("a", 64) + "/evidence", "/v1/admin/obs/incidents/platform_alert/" + strings.Repeat("a", 64) + "/triage-history"} {
		rec := e.Do(t, "GET", path, nil, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}
