package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestObsHealthSignalPartialFailureAndRecovery(t *testing.T) {
	e := gapEnv(t)
	var recovered atomic.Bool
	p := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if strings.Contains(query, "failures_total") && !recovered.Load() {
			http.Error(w, "private upstream detail", 503)
			return
		}
		value := int64(8)
		if strings.Contains(query, "timestamp(") || strings.Contains(query, "last_evaluation_timestamp_seconds") {
			value = time.Now().Unix()
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,"%d"]}]}}`, value)
	}))
	defer p.Close()
	*(*e.S()).PromqlClient() = promql.NewClient(p.URL, p.Client())
	for _, recover := range []bool{false, true} {
		recovered.Store(recover)
		rec := e.Do(t, "GET", "/v1/admin/obs/health", nil, nil)
		var got api.ObsHealthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if rec.Code != 200 || got.PrometheusAvailable != recover || got.AuditLogWriteTotal5m != 8 || got.AlertsFiring != 8 || got.AuditLogCoverageRatio5m != 1 {
			t.Fatalf("recovered=%v status=%d body=%s", recover, rec.Code, rec.Body.String())
		}
		wantState, wantValue := api.ObsHealthSignalUnavailable, int64(0)
		if recover {
			wantState, wantValue = api.ObsHealthSignalHealthy, 8
		}
		if got.Signals["audit_log_write_failures_5m"].State != wantState || got.AuditLogWriteFailures5m != wantValue || len(got.Signals) != 3 || strings.Contains(rec.Body.String(), "private upstream") {
			t.Fatalf("recovered=%v body=%s", recover, rec.Body.String())
		}
	}
}

type obsHealthFailedAudit struct{ Store state.Store }

func (*obsHealthFailedAudit) OperatorAuditTraceCoverage(context.Context, time.Time) (float64, error) {
	return 0, errors.New("database unavailable")
}

func TestObsHealthSignalMetadataDoesNotHideDatabaseFailure(t *testing.T) {
	e := gapEnv(t)
	*(*e.S()).Store() = statetest.NewStore(&obsHealthFailedAudit{Store: *(*e.S()).Store()})
	rec := e.Do(t, "GET", "/v1/admin/obs/health", nil, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
