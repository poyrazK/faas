package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestObsAlertResponseTimes(t *testing.T) {
	e := gapEnv(t)
	path := operatorobs.AlertResponseTimesPath
	rec := e.Do(t, "GET", path, nil, nil)
	var out api.ObsAlertResponseTimes
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || out.Totals != nil || out.Groups == nil || out.Collection.CollectionState != "disabled" {
		t.Fatalf("unknown: %d %s", rec.Code, rec.Body)
	}
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second)
	store := *(*e.S()).Store()
	episode := seedObsAlert(t, store, now, true)
	if _, err := store.MutateOperatorAlertTriage(t.Context(), state.OperatorAlertTriageMutation{EpisodeID: episode.ID, ActorID: "11111111-1111-4111-8111-111111111111", Action: "acknowledge", Now: now}); err != nil {
		t.Fatal(err)
	}
	rec = e.Do(t, "GET", path, nil, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || out.Totals == nil || out.Totals.Episodes != 1 || out.Totals.Acknowledgment.Samples != 1 || out.Totals.UnknownObservation != 1 || out.Scope != "retained_activation_cohort" {
		t.Fatalf("timing: %d %s", rec.Code, rec.Body)
	}
	for _, q := range []string{"?limit=1", "?since=bad", "?since=2000-01-01T00:00:00Z", "?since=2999-01-01T00:00:00Z", "?since=&since="} {
		if rec := e.Do(t, "GET", path+q, nil, nil); rec.Code != 400 {
			t.Fatalf("bad query: %d %s", rec.Code, rec.Body)
		}
	}
	*(*e.S()).Store() = statetest.NewStore(&obsResponseTimingFailure{Store: store})
	rec = e.Do(t, "GET", path, nil, nil)
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "private timing") || strings.Contains(rec.Body.String(), "p50_seconds") {
		t.Fatalf("failure: %d %s", rec.Code, rec.Body)
	}
	*(*e.S()).Store() = statetest.NewStore(&obsResponseTimingFailure{Store: store, bound: true})
	rec = e.Do(t, "GET", path, nil, nil)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "narrow since") {
		t.Fatalf("bounded: %d %s", rec.Code, rec.Body)
	}
	e = apidserver.NewObsEnv(t, api.ScopesReadSurface, "ops@faas.dev", "customer@faas.dev")
	if rec := e.Do(t, "GET", path, nil, nil); rec.Code != 403 {
		t.Fatalf("customer: %d %s", rec.Code, rec.Body)
	}
}

type obsResponseTimingFailure struct {
	Store state.Store
	bound bool
}

func (s *obsResponseTimingFailure) OperatorAlertResponseTimes(context.Context, state.OperatorAlertResponseTimesFilter) (state.OperatorAlertResponseTimesSnapshot, error) {
	if s.bound {
		return state.OperatorAlertResponseTimesSnapshot{}, &state.OperatorAlertResponseTimesLimitError{Resource: "episodes", Limit: api.ObsAlertResponseTimesEpisodesMax}
	}
	return state.OperatorAlertResponseTimesSnapshot{}, errors.New("private timing query detail")
}
