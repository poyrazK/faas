package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestObsAlertCollectionHistoryReadOnlyAndFailures(t *testing.T) {
	e := gapEnv(t)
	path := operatorobs.AlertCollectionHistoryPath
	rec := e.Do(t, "GET", path, nil, nil)
	var out api.ObsAlertCollectionHistory
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Coverage.State != "unknown" || len(out.Items) != 1 || out.Collection.CollectionState != "disabled" {
		t.Fatalf("unknown: %d %s", rec.Code, rec.Body)
	}
	store := *(*e.S()).Store()
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second)
	episode := seedObsAlert(t, store, now.Add(-time.Minute), true)
	if err := store.RecordOperatorAlertFailure(t.Context(), now, "timeout"); err != nil {
		t.Fatal(err)
	}
	prior, c, err := store.OperatorAlertEpisode(t.Context(), episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec = e.Do(t, "GET", path, nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Coverage.Successes != 1 || out.Coverage.Failures != 1 || out.Coverage.State != "partial" || out.Items[0].Reason != "timeout" {
		t.Fatalf("timeline: %d %s", rec.Code, rec.Body)
	}
	after, current, err := store.OperatorAlertEpisode(t.Context(), episode.ID)
	if err != nil || !reflect.DeepEqual(prior, after) || c != current {
		t.Fatal("GET changed episode or collection")
	}
	for _, q := range []string{"?", "?limit=1", "?since=", "?since=bad", "?since=&since=", "?since=2000-01-01T00:00:00Z", "?since=2999-01-01T00:00:00Z", "?since=%zz"} {
		if rec := e.Do(t, "GET", path+q, nil, nil); rec.Code != 400 {
			t.Fatalf("bad query: %s %d %s", q, rec.Code, rec.Body)
		}
	}
	for _, q := range []string{"?details=", "?details=1", "?details=True", "?details=true&details=false", "?details=true&unknown=1", "?details=true&since="} {
		if rec := e.Do(t, "GET", path+q, nil, nil); rec.Code != 400 {
			t.Fatalf("bad details query: %s %d %s", q, rec.Code, rec.Body)
		}
	}
	*(*e.S()).Store() = statetest.NewStore(&obsCollectionHistoryFailure{Store: store})
	rec = e.Do(t, "GET", path, nil, nil)
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "private database") {
		t.Fatalf("failure redaction: %d %s", rec.Code, rec.Body)
	}
	*(*e.S()).Store() = statetest.NewStore(&obsCollectionHistoryFailure{Store: store, bound: true})
	rec = e.Do(t, "GET", path, nil, nil)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "narrow since") {
		t.Fatalf("bound: %d %s", rec.Code, rec.Body)
	}
}

func TestObsAlertCollectionHistoricalRejectionDetails(t *testing.T) {
	e := gapEnv(t)
	store := *(*e.S()).Store()
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second)
	d := api.ObsAlertCollectionRejection{Scope: "rejected_snapshot", Reason: "no_alert_rules_observed", Items: []api.ObsRuleHealthItem{}, Limit: api.ObsAlertCollectionRejectionItemsMax}
	if err := store.RecordOperatorAlertFailure(t.Context(), now.Add(-time.Second), "timeout"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperatorAlertRejection(t.Context(), now, d); err != nil {
		t.Fatal(err)
	}
	before, err := store.OperatorAlertCollectionStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, details := range []string{"true", "false"} {
		rec := e.Do(t, "GET", operatorobs.AlertCollectionHistoryPath+"?details="+details, nil, nil)
		var out api.ObsAlertCollectionHistory
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("response: %d %s", rec.Code, rec.Body)
		}
		if details == "false" {
			if out.FailureDetails != nil {
				t.Fatal("unrequested details exposed")
			}
			continue
		}
		if out.FailureDetails == nil || out.FailureDetails.TotalFailures != 2 || len(out.FailureDetails.Items) != 2 || out.FailureDetails.Items[0].DiagnosticsState != "recorded" || out.FailureDetails.Items[0].Diagnostics == nil || out.FailureDetails.Items[0].Diagnostics.Reason != "no_alert_rules_observed" || out.FailureDetails.Items[1].DiagnosticsState != "unknown" || out.FailureDetails.Items[1].Diagnostics != nil {
			t.Fatalf("details: %s", rec.Body)
		}
	}
	after, err := store.OperatorAlertCollectionStatus(t.Context())
	if err != nil || after != before {
		t.Fatalf("GET changed evidence: %+v %v", after, err)
	}
}

func TestObsAlertCollectionHistoryAuthorization(t *testing.T) {
	path := operatorobs.AlertCollectionHistoryPath
	for _, tc := range []struct {
		scopes []string
		email  string
	}{{api.ScopesReadSurface, "customer@faas.dev"}, {api.ScopesAdminOnly, "rogue@faas.dev"}} {
		e := apidserver.NewObsEnv(t, tc.scopes, "ops@faas.dev", tc.email)
		if rec := e.Do(t, "GET", path, nil, nil); rec.Code != 403 {
			t.Fatalf("authorization: %d %s", rec.Code, rec.Body)
		}
	}
	h, account, manager, sid := apidserver.SetupMW(t, api.PlanPro, true)
	pending := apidserver.ReissueWithMFAFlag(t, manager, sid, account.ID, true)
	rec := apidserver.CookieDo(t, h, pending, http.MethodGet, path, nil)
	var p api.Problem
	if rec.Code != 403 || json.Unmarshal(rec.Body.Bytes(), &p) != nil || p.Code != api.CodeMFARequired {
		t.Fatalf("MFA: %d %s", rec.Code, rec.Body)
	}
}

type obsCollectionHistoryFailure struct {
	Store state.Store
	bound bool
}

func (s *obsCollectionHistoryFailure) OperatorAlertCollectionHistory(context.Context, state.OperatorAlertCollectionHistoryFilter) (state.OperatorAlertCollectionHistorySnapshot, error) {
	if s.bound {
		return state.OperatorAlertCollectionHistorySnapshot{}, &state.OperatorAlertCollectionHistoryLimitError{Limit: api.ObsAlertCollectionHistoryOutcomesMax}
	}
	return state.OperatorAlertCollectionHistorySnapshot{}, errors.New("private database detail")
}

func TestObsAnalyticCollectionCoverageLimit(t *testing.T) {
	e := gapEnv(t)
	store := *(*e.S()).Store()
	*(*e.S()).Store() = statetest.NewStore(&obsAnalyticCoverageLimit{Store: store})
	for _, path := range []string{operatorobs.AlertRecurrencePath, operatorobs.AlertResponseTimesPath} {
		rec := e.DoOperator(t, "GET", path, nil, nil)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "narrow since") || !strings.Contains(rec.Body.String(), "100000") {
			t.Fatalf("coverage bound %s: %d %s", path, rec.Code, rec.Body)
		}
	}
}

type obsAnalyticCoverageLimit struct{ Store state.Store }

func (*obsAnalyticCoverageLimit) OperatorAlertRecurrence(context.Context, state.OperatorAlertRecurrenceFilter) (state.OperatorAlertRecurrenceSnapshot, error) {
	return state.OperatorAlertRecurrenceSnapshot{}, &state.OperatorAlertCollectionHistoryLimitError{Limit: api.ObsAlertCollectionHistoryOutcomesMax}
}
func (*obsAnalyticCoverageLimit) OperatorAlertResponseTimes(context.Context, state.OperatorAlertResponseTimesFilter) (state.OperatorAlertResponseTimesSnapshot, error) {
	return state.OperatorAlertResponseTimesSnapshot{}, &state.OperatorAlertCollectionHistoryLimitError{Limit: api.ObsAlertCollectionHistoryOutcomesMax}
}
