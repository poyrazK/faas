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
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestObsAlertWorkloadSummary(t *testing.T) {
	e := gapEnv(t)
	path := operatorobs.AlertWorkloadSummaryPath
	rec := e.Do(t, "GET", path, nil, nil)
	var never api.ObsAlertWorkloadSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &never); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || never.Counts != nil || never.Groups == nil || never.Collection.CollectionState != "disabled" {
		t.Fatalf("never: %d %s", rec.Code, rec.Body)
	}
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Second)
	store := *(*e.S()).Store()
	episode := seedObsAlert(t, store, now, true)
	rec = e.Do(t, "GET", path, nil, nil)
	var out api.ObsAlertWorkloadSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || out.Scope != "retained_platform_alert_episodes" || out.Counts == nil || out.Counts.Unknown != 1 || out.Counts.NeedsAttention != 1 || out.OldestUnowned == nil || out.OldestUnowned.IncidentID != "platform_alert/"+episode.ID {
		t.Fatalf("retained: %d %s", rec.Code, rec.Body)
	}
	for _, q := range []string{"?limit=1", "?since=2026-01-01", "?type=deployment", "?cursor=bad"} {
		if rec := e.Do(t, "GET", path+q, nil, nil); rec.Code != 400 {
			t.Fatalf("ignored query: %d %s", rec.Code, rec.Body)
		}
	}
	*(*e.S()).Store() = statetest.NewStore(&obsWorkloadFailureStore{Store: store})
	rec = e.Do(t, "GET", path, nil, nil)
	if rec.Code != 503 || strings.Contains(rec.Body.String(), "private workload") || strings.Contains(rec.Body.String(), "needs_attention") {
		t.Fatalf("failure: %d %s", rec.Code, rec.Body)
	}
	e = apidserver.NewObsEnv(t, api.ScopesReadSurface, "ops@faas.dev", "customer@faas.dev")
	if rec := e.Do(t, "GET", path, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("customer: %d %s", rec.Code, rec.Body)
	}
}

type obsWorkloadFailureStore struct{ Store state.Store }

func (s *obsWorkloadFailureStore) OperatorAlertWorkload(context.Context, state.OperatorAlertWorkloadFilter) (state.OperatorAlertWorkloadSnapshot, error) {
	return state.OperatorAlertWorkloadSnapshot{}, errors.New("private workload storage detail")
}
