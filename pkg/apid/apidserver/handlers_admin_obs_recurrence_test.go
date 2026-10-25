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

func TestObsAlertRecurrenceReadOnlyAndErrors(t *testing.T) {
	e := gapEnv(t)
	path := operatorobs.AlertRecurrencePath
	rec := e.DoOperator(t, "GET", path, nil, nil)
	var out api.ObsAlertRecurrence
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Totals != nil || out.Items == nil || out.Collection.CollectionState != "disabled" {
		t.Fatalf("unknown: %d %s", rec.Code, rec.Body)
	}
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second)
	store := *(*e.S()).Store()
	episode := seedObsAlert(t, store, now, true)
	if _, err := store.MutateOperatorAlertTriage(t.Context(), state.OperatorAlertTriageMutation{EpisodeID: episode.ID, ActorID: "11111111-1111-4111-8111-111111111111", Action: "claim", Now: now}); err != nil {
		t.Fatal(err)
	}
	prior, collection, err := store.OperatorAlertEpisode(t.Context(), episode.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec = e.DoOperator(t, "GET", path+"?since="+now.Add(-time.Hour).Format(time.RFC3339), nil, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Totals == nil || out.Totals.Series != 1 || out.Totals.Metrics.Episodes != 1 || out.Totals.Metrics.UnknownObservation != 1 || out.Totals.Metrics.RepeatActivations != 0 || out.Items[0].RecentEpisodes[0].EpisodeID != episode.ID || out.Totals.Metrics.Duration.P50Seconds != nil {
		t.Fatalf("recurrence: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "11111111-1111-4111-8111-111111111111") || strings.Contains(rec.Body.String(), "owner_account_id") {
		t.Fatalf("workflow identity leaked: %s", rec.Body)
	}
	after, current, err := store.OperatorAlertEpisode(t.Context(), episode.ID)
	if err != nil || !reflect.DeepEqual(after, prior) || current != collection {
		t.Fatal("read changed episode or collection")
	}
	for _, q := range []string{"?", "?limit=1", "?since=", "?since=bad", "?since=2000-01-01T00:00:00Z", "?since=2999-01-01T00:00:00Z", "?since=&since=", "?since=%zz", "?since=x;other=y"} {
		if rec := e.DoOperator(t, "GET", path+q, nil, nil); rec.Code != 400 {
			t.Fatalf("bad query %s: %d %s", q, rec.Code, rec.Body)
		}
	}
	*(*e.S()).Store() = statetest.NewStore(&obsRecurrenceFailure{Store: store})
	rec = e.DoOperator(t, "GET", path, nil, nil)
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "private recurrence") || strings.Contains(rec.Body.String(), "repeat_activations") {
		t.Fatalf("failure: %d %s", rec.Code, rec.Body)
	}
	*(*e.S()).Store() = statetest.NewStore(&obsRecurrenceFailure{Store: store, bound: true})
	rec = e.DoOperator(t, "GET", path, nil, nil)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "narrow since") || !strings.Contains(rec.Body.String(), "1000") {
		t.Fatalf("bounded: %d %s", rec.Code, rec.Body)
	}
}

func TestObsAlertRecurrenceAuthorization(t *testing.T) {
	path := operatorobs.AlertRecurrencePath
	for _, tc := range []struct {
		scopes []string
		email  string
	}{{api.ScopesReadSurface, "customer@faas.dev"}, {api.ScopesAdminOnly, "rogue@faas.dev"}} {
		e := apidserver.NewObsEnv(t, tc.scopes, "ops@faas.dev", tc.email)
		if rec := e.Do(t, "GET", path, nil, nil); rec.Code != 403 {
			t.Fatalf("authorization: %d %s", rec.Code, rec.Body)
		}
	}
	h, account, manager, sessionID := apidserver.SetupMW(t, api.PlanPro, true)
	pending := apidserver.ReissueWithMFAFlag(t, manager, sessionID, account.ID, true)
	rec := apidserver.CookieDo(t, h, pending, http.MethodGet, path, nil)
	var problem api.Problem
	if rec.Code != 403 || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != api.CodeMFARequired {
		t.Fatalf("MFA: %d %s", rec.Code, rec.Body)
	}
}

type obsRecurrenceFailure struct {
	Store state.Store
	bound bool
}

func (s *obsRecurrenceFailure) OperatorAlertRecurrence(context.Context, state.OperatorAlertRecurrenceFilter) (state.OperatorAlertRecurrenceSnapshot, error) {
	if s.bound {
		return state.OperatorAlertRecurrenceSnapshot{}, &state.OperatorAlertRecurrenceLimitError{Limit: api.ObsAlertRecurrenceEpisodesMax}
	}
	return state.OperatorAlertRecurrenceSnapshot{}, errors.New("private recurrence query detail")
}
