// adr: 20261022203523
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func perInstancePolicy(n int) api.UpdateAppRequest {
	return api.UpdateAppRequest{
		ScalingPolicy:    &api.ScalingPolicy{ScaleOutCooldownS: 5, ScaleInCooldownS: 60, PerInstanceConcurrency: n},
		SetScalingPolicy: true,
	}
}

// TestUpdateAppPerInstanceConcurrency covers the gate, the plan bound and the
// effective limits apid reports for scaling_policy.per_instance_concurrency.
func TestUpdateAppPerInstanceConcurrency(t *testing.T) {
	for _, tc := range []struct {
		name         string
		plan         api.Plan
		accounts     string // FAAS_PER_INSTANCE_CONCURRENCY_ACCOUNTS; "self" lists the caller
		value        int
		wantStatus   int
		wantDetail   string
		wantPerInst  int
		wantWaitMS   int64
		wantObserved int64
	}{
		{name: "unlisted account sees an unknown field", plan: api.PlanPro, value: 2, wantStatus: http.StatusUnprocessableEntity, wantDetail: "unknown field(s): per_instance_concurrency"},
		{name: "unlisted negative value sees an unknown field too", plan: api.PlanPro, value: -1, wantStatus: http.StatusUnprocessableEntity, wantDetail: "unknown field(s): per_instance_concurrency"},
		{name: "zero needs no listing", plan: api.PlanPro, value: 0, wantStatus: http.StatusOK, wantPerInst: 25, wantWaitMS: api.ConcurrencyQueuePaidDefaultWait.Milliseconds()},
		{name: "listed account", plan: api.PlanPro, accounts: "self", value: 2, wantStatus: http.StatusOK, wantPerInst: 2, wantWaitMS: api.PerInstanceConcurrencyQueueDefaultWait.Milliseconds()},
		{name: "every account", plan: api.PlanScale, accounts: "*", value: 2, wantStatus: http.StatusOK, wantPerInst: 2, wantWaitMS: api.PerInstanceConcurrencyQueueDefaultWait.Milliseconds()},
		{name: "plan bound", plan: api.PlanFree, accounts: "*", value: 4, wantStatus: http.StatusOK, wantPerInst: 4, wantWaitMS: api.PerInstanceConcurrencyQueueDefaultWait.Milliseconds()},
		{name: "above the plan bound", plan: api.PlanFree, accounts: "*", value: 5, wantStatus: http.StatusUnprocessableEntity, wantDetail: "per_instance_concurrency must be between 0 and 4", wantObserved: 5},
		{name: "negative", plan: api.PlanPro, accounts: "*", value: -1, wantStatus: http.StatusUnprocessableEntity, wantDetail: "per_instance_concurrency must be between 0 and 25", wantObserved: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.Setup(t, tc.plan)
			accounts := tc.accounts
			if accounts == "self" {
				accounts = e.Acct().ID
			}
			t.Setenv("FAAS_PER_INSTANCE_CONCURRENCY_ACCOUNTS", accounts)
			apidserver.MustSeedApp(t, e, "gunicorn")
			rec := e.Do(t, http.MethodPatch, "/v1/apps/gunicorn", perInstancePolicy(tc.value), nil)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body)
			}
			if tc.wantStatus != http.StatusOK {
				var problem api.Problem
				if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
					t.Fatalf("decode problem: %v", err)
				}
				if !strings.Contains(problem.Detail, tc.wantDetail) {
					t.Fatalf("detail = %q, want it to contain %q", problem.Detail, tc.wantDetail)
				}
				if tc.wantObserved != 0 && (problem.Observed == nil || *problem.Observed != tc.wantObserved || problem.Limit == nil) {
					t.Fatalf("limit/observed = %v/%v, want observed %d", problem.Limit, problem.Observed, tc.wantObserved)
				}
				return
			}
			var out api.AppResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode app: %v", err)
			}
			if out.ScalingPolicy == nil || out.ScalingPolicy.PerInstanceConcurrency != tc.value {
				t.Fatalf("scaling_policy = %+v, want per_instance_concurrency %d", out.ScalingPolicy, tc.value)
			}
			if out.EffectiveLimits.ConcurrencyPerInstance != tc.wantPerInst || out.EffectiveLimits.ConcurrencyQueueWaitMS != tc.wantWaitMS {
				t.Fatalf("effective limits = %d per instance, %d ms wait; want %d, %d ms",
					out.EffectiveLimits.ConcurrencyPerInstance, out.EffectiveLimits.ConcurrencyQueueWaitMS, tc.wantPerInst, tc.wantWaitMS)
			}
		})
	}
}

// TestUpdateAppPerInstanceConcurrencySurvivesDelisting pins that removing an
// account from the gate never breaks a read-modify-write of its other policy
// fields: the stored value may be kept or cleared, only a new one is refused.
func TestUpdateAppPerInstanceConcurrencySurvivesDelisting(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "delisted")
	t.Setenv("FAAS_PER_INSTANCE_CONCURRENCY_ACCOUNTS", "*")
	if rec := e.Do(t, http.MethodPatch, "/v1/apps/delisted", perInstancePolicy(2), nil); rec.Code != http.StatusOK {
		t.Fatalf("listed write: %d %s", rec.Code, rec.Body)
	}
	t.Setenv("FAAS_PER_INSTANCE_CONCURRENCY_ACCOUNTS", "")
	keep := perInstancePolicy(2)
	keep.ScalingPolicy.MaxQueueDepth = 16
	for _, step := range []struct {
		name string
		req  api.UpdateAppRequest
		want int
	}{
		{name: "keep the stored value", req: keep, want: http.StatusOK},
		{name: "change it", req: perInstancePolicy(3), want: http.StatusUnprocessableEntity},
		{name: "clear it", req: perInstancePolicy(0), want: http.StatusOK},
	} {
		if rec := e.Do(t, http.MethodPatch, "/v1/apps/delisted", step.req, nil); rec.Code != step.want {
			t.Fatalf("%s: status %d, want %d: %s", step.name, rec.Code, step.want, rec.Body)
		}
	}
}
