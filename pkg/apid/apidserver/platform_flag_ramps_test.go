package apidserver_test

// adr: 20261021080640 — automatic ramps: the worker advances a rollout stage
// while the flag's health routes stay healthy and pauses on a breach.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

// rampProm answers apid_request_total queries: 1000 requests, of which
// failures are 5xx.
func rampProm(t *testing.T, failures *atomic.Int64) *promql.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if query == "" {
			query = r.FormValue("query")
		}
		value := int64(1000)
		if strings.Contains(query, `code="err"`) {
			value = failures.Load()
		}
		if !strings.Contains(query, `apid_request_total`) || !strings.Contains(query, `GET /v1/service-map`) {
			t.Errorf("unexpected health query %q", query)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1728000000,"%d"]}]}}`, value)
	}))
	t.Cleanup(server.Close)
	return promql.NewClient(server.URL, server.Client())
}

func rampDefinition(rollout int) platformflags.Definition {
	value := true
	return platformflags.Definition{Enabled: true, Rules: []platformflags.Rule{{
		ID: "rollout", Rollout: &rollout, Value: &value,
		Ramp: &platformflags.Ramp{Stages: []int{100, 1000, 10000}, IntervalSeconds: 300, Max5xxBasisPoints: 100, MinRequests: 50, OnBreach: platformflags.RampRollback},
	}}}
}

func storedRule(t *testing.T, e apidserver.TestEnv) (platformflags.Rule, int64) {
	t.Helper()
	row, err := (*e.Store()).GetRuntimeConfig(context.Background(), platformflags.ConfigKey(platformflags.ServiceMap), state.RuntimeConfigScopeGlobal, "")
	if err != nil {
		t.Fatal(err)
	}
	def, err := platformflags.ParseDefinition(row.DesiredValue)
	if err != nil || len(def.Rules) != 1 {
		t.Fatalf("stored definition = %+v, %v", def, err)
	}
	return def.Rules[0], row.Version
}

func TestPlatformFlagRampAdvancesThenRollsBackOnABreach(t *testing.T) {
	e := apidserver.NewObsEnv(t, []string{"admin"}, "ops@faas.dev", "ops@faas.dev")
	s := *e.S()
	var failures atomic.Int64
	failures.Store(5) // 50 bp
	*s.PromqlClient() = rampProm(t, &failures)
	if code := patchPlatformConfig(t, e, platformflags.ConfigKey(platformflags.ServiceMap), rampDefinition(100), 0); code != http.StatusOK {
		t.Fatalf("write ramp = %d", code)
	}
	ctx := context.Background()

	s.AdvancePlatformFlagRamps(ctx, time.Now())
	if rule, version := storedRule(t, e); version != 1 || *rule.Rollout != 100 {
		t.Fatalf("a stage younger than its interval must hold: rollout %d v%d", *rule.Rollout, version)
	}

	s.AdvancePlatformFlagRamps(ctx, time.Now().Add(10*time.Minute))
	rule, version := storedRule(t, e)
	if version != 2 || *rule.Rollout != 1000 || rule.Ramp.Paused {
		t.Fatalf("healthy ramp after its interval = rollout %d v%d paused=%t, want 1000 at v2", *rule.Rollout, version, rule.Ramp.Paused)
	}
	if def, live, _ := s.PlatformFlagSet().Snapshot().Definition(platformflags.ServiceMap); live != 2 || *def.Rules[0].Rollout != 1000 {
		t.Fatalf("apid must apply the advanced stage: v%d", live)
	}

	failures.Store(50) // 500 bp, over the 100 bp threshold
	s.AdvancePlatformFlagRamps(ctx, time.Now().Add(20*time.Minute))
	rule, version = storedRule(t, e)
	if version != 3 || *rule.Rollout != 100 || !rule.Ramp.Paused || !strings.Contains(rule.Ramp.PausedReason, "500 bp") {
		t.Fatalf("breach = rollout %d v%d paused=%t reason=%q, want rolled back to 100 and paused", *rule.Rollout, version, rule.Ramp.Paused, rule.Ramp.PausedReason)
	}

	failures.Store(0)
	s.AdvancePlatformFlagRamps(ctx, time.Now().Add(time.Hour))
	if _, version := storedRule(t, e); version != 3 {
		t.Fatalf("a paused ramp must wait for an operator, got v%d", version)
	}
}

func TestPlatformFlagRampHoldsWithoutPrometheusAndNeedsHealthRoutes(t *testing.T) {
	e := apidserver.NewObsEnv(t, []string{"admin"}, "ops@faas.dev", "ops@faas.dev")
	s := *e.S()
	*s.PromqlClient() = nil
	if code := patchPlatformConfig(t, e, platformflags.ConfigKey(platformflags.ServiceMap), rampDefinition(100), 0); code != http.StatusOK {
		t.Fatalf("write ramp = %d", code)
	}
	s.AdvancePlatformFlagRamps(context.Background(), time.Now().Add(time.Hour))
	if rule, version := storedRule(t, e); version != 1 || *rule.Rollout != 100 {
		t.Fatalf("without Prometheus the ramp must hold: rollout %d v%d", *rule.Rollout, version)
	}
	if code := patchPlatformConfig(t, e, platformflags.ConfigKey(platformflags.ApplicationAuth), rampDefinition(100), 0); code != http.StatusConflict {
		t.Fatalf("ramp on a flag without health routes = %d, want 409", code)
	}
}
