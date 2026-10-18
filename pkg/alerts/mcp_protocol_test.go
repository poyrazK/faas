package alerts_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

type mcpProtocolProm struct {
	app                     string
	count, value, sustained float64
	countErr, sustainedErr  error
	queries                 []string
}

func (p *mcpProtocolProm) QueryScalar(_ context.Context, q string) (float64, error) {
	if !strings.Contains(q, `app_id="`+p.app+`"`) {
		return 0, errors.New("query escaped app scope")
	}
	p.queries = append(p.queries, q)
	if strings.HasPrefix(q, "min_over_time") {
		return p.sustained, p.sustainedErr
	}
	if strings.HasPrefix(q, "sum(increase") {
		return p.count, p.countErr
	}
	return p.value, nil
}
func TestMCPProtocolCustomerAlertEvaluation(t *testing.T) {
	for _, metric := range []string{api.MetricMCPProtocolErrorRate, api.MetricMCPProtocolP95} {
		for _, tc := range []struct {
			name                          string
			count, value, sustained       float64
			countErr, sustainedErr        error
			fired, insufficient, degraded int
		}{
			{name: "sustained breach", count: 100, value: 30, sustained: 1, fired: 1},
			{name: "healthy", count: 100, value: 1},
			{name: "idle", count: 0, value: 30, sustained: 1, insufficient: 1},
			{name: "sparse", count: 19, value: 30, sustained: 1, insufficient: 1},
			{name: "no observations", countErr: promql.ErrNoData, insufficient: 1},
			{name: "history interrupted", count: 100, value: 30, sustainedErr: promql.ErrNoData, insufficient: 1},
			{name: "backend unavailable", countErr: errors.New("unavailable"), degraded: 1},
		} {
			t.Run(metric+"/"+tc.name, func(t *testing.T) {
				base := state.NewMemStore()
				rule, identity, _ := seedRule(t, base, state.AlertMetric(metric), state.AlertGt, 10)
				prom := &mcpProtocolProm{app: rule.AppID, count: tc.count, value: tc.value, sustained: tc.sustained, countErr: tc.countErr, sustainedErr: tc.sustainedErr}
				now := time.Now().Add(11 * time.Minute)
				dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
				evaluator := alerts.NewEvaluator(alerts.EvaluatorOptions{Store: base, PromQL: prom, Identity: func() *age.X25519Identity { return identity }, Dispatcher: dispatch, Now: func() time.Time { return now }, Log: discardLog()})
				stats, err := evaluator.RunOnce(t.Context())
				if err != nil || stats.Fired != tc.fired || stats.SkippedInsufficient != tc.insufficient || stats.SkippedDegraded != tc.degraded {
					t.Fatalf("unexpected stats: %+v %v", stats, err)
				}
				if dispatch.callCount() != tc.fired {
					t.Fatal("wrong notification count")
				}
				if tc.fired > 0 {
					_, _ = evaluator.RunOnce(t.Context())
					if dispatch.callCount() != 1 {
						t.Fatal("cooldown did not deduplicate")
					}
				}
			})
		}
	}
}
