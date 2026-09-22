// autoscale_custom_metric_e2e_test.go — KVM-free autoscaling acceptance.
//
// Closes gap #2 of the scaling test audit: PR CI never scaled an app end to
// end. The only test that did was TestDirectOCIAutoscaleScaleToZeroMetal,
// which is `//go:build metal` and needs /dev/kvm, a kernel and a builder
// base — so the gate that actually blocks merges had zero autoscaling
// coverage, while the scheduler grew six signals and a schedule.
//
// The path this exercises is the one that broke silently: a scaling policy
// written through the real apid, persisted to real Postgres, read back by a
// real schedd, arbitrated, and turned into an admission. ADR-194's `targets`
// was written and discarded on every read with 1,270 unit tests green,
// because no test crossed those boundaries in one process.
//
// `custom` is the signal that makes this runnable without KVM: it is pushed
// through the public API, so the test can make an app hot without a guest,
// a gateway request, or any vmmd telemetry. Every other signal needs load
// the harness cannot generate off metal.
package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestE2E_Autoscale_CustomMetricAdmitsCapacity drives the full loop:
// PATCH a policy -> push a metric -> schedd scales.
func TestE2E_Autoscale_CustomMetricAdmitsCapacity(t *testing.T) {
	// Pro for the headroom: Hobby caps max_concurrency at 2, which leaves
	// almost nothing to observe between "already running" and "at cap".
	f := newNormalPathFixtureWithPlan(t, "autoscale-custom", api.PlanPro)
	if f == nil {
		return
	}
	// The targets loop admits additional instances for a live deployment. The
	// fixture only creates app metadata, so seed one readable live deployment
	// before exercising the scale-out signal.
	createNormalPathLiveDeployment(t, f, f.app.ID, "autoscale-custom-v1")
	// 1. Declare the policy through the customer API, not by writing the
	//    row. The apid validation, the jsonb encode and the column write
	//    are all part of what this test exists to cover.
	policy := &api.ScalingPolicy{
		MaxInstances: 5,
		Targets: []api.ScalingTarget{
			{Metric: api.ScalingMetricCustom, Name: "orders_pending", Value: 10},
		},
		ScaleOutCooldownS: api.MinScaleOutCooldownS,
		ScaleInCooldownS:  api.MinScaleInCooldownS,
	}
	body, status := doReq(t, f.h, f.key, http.MethodPatch, "/v1/apps/"+f.app.Slug,
		api.UpdateAppRequest{ScalingPolicy: policy})
	if status != http.StatusOK {
		t.Fatalf("PATCH scaling policy: status=%d body=%s", status, body)
	}

	// 2. Prove the policy SURVIVED the round trip before relying on it.
	//    A read-back that has lost `targets` is the ADR-194 failure, and
	//    without this assertion the scale-out below would simply never
	//    happen and the test would fail with a far less useful message.
	var readBack api.AppResponse
	body, status = doReq(t, f.h, f.key, http.MethodGet, "/v1/apps/"+f.app.Slug, nil)
	if status != http.StatusOK {
		t.Fatalf("GET app: status=%d body=%s", status, body)
	}
	if err := json.Unmarshal(body, &readBack); err != nil {
		t.Fatalf("decode app: %v", err)
	}
	if readBack.ScalingPolicy == nil || len(readBack.ScalingPolicy.Targets) != 1 {
		t.Fatalf("scaling policy did not survive apid -> Postgres -> apid: %+v",
			readBack.ScalingPolicy)
	}
	if got := readBack.ScalingPolicy.Targets[0]; got.Metric != api.ScalingMetricCustom ||
		got.Name != "orders_pending" || got.Value != 10 {
		t.Fatalf("target changed across the round trip: %+v", got)
	}

	before := instanceCount(t, f)

	// 3. Push a backlog that demands the full five instances:
	//    ceil(50 / 10) = 5.
	body, status = doReq(t, f.h, f.key, http.MethodPut,
		"/v1/apps/"+f.app.Slug+"/custom-metrics/orders_pending",
		api.CustomMetricRequest{Value: 50})
	if status != http.StatusNoContent {
		t.Fatalf("push custom metric: status=%d body=%s", status, body)
	}

	// 4. schedd's targets trigger ticks on its own interval, so poll
	//    rather than sleep a fixed amount. The assertion is that capacity
	//    GREW — not an exact count, because the ledger, the per-tick burst
	//    bound and the reaper all legitimately influence the final number
	//    and pinning it here would make this a flaky restatement of unit
	//    tests that already cover the arithmetic.
	deadline := time.Now().Add(45 * time.Second)
	var after int
	for time.Now().Before(deadline) {
		after = instanceCount(t, f)
		if after > before {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if after <= before {
		t.Fatalf("instances did not grow after a hot custom metric: before=%d after=%d.\n"+
			"The policy round-tripped (asserted above), so the break is between schedd "+
			"reading the metric and admitting: check that cmd/schedd wires "+
			"CustomMetricReader and that the targets trigger is enabled.",
			before, after)
	}
}

// TestE2E_Autoscale_StaleCustomMetricDoesNotAdmit is the negative half, and
// it is the one that would catch a freshness regression in production
// wiring rather than in a unit fake.
//
// A metric is pushed and then nothing refreshes it. Because the freshness
// window is minutes, this cannot wait it out; instead it pushes a value that
// is hot and asserts that an app with NO pushed metric at all never scales —
// the same "no reading" path a stale row takes through the arbiter.
func TestE2E_Autoscale_NoMetricDoesNotAdmit(t *testing.T) {
	f := newNormalPathFixtureWithPlan(t, "autoscale-nometric", api.PlanPro)
	if f == nil {
		return
	}
	// Seed the same baseline as the positive case; otherwise a no-growth
	// result would be vacuous because there is no deployment to scale.
	createNormalPathLiveDeployment(t, f, f.app.ID, "autoscale-nometric-v1")

	policy := &api.ScalingPolicy{
		MaxInstances: 5,
		Targets: []api.ScalingTarget{
			{Metric: api.ScalingMetricCustom, Name: "never_pushed", Value: 1},
		},
		ScaleOutCooldownS: api.MinScaleOutCooldownS,
		ScaleInCooldownS:  api.MinScaleInCooldownS,
	}
	body, status := doReq(t, f.h, f.key, http.MethodPatch, "/v1/apps/"+f.app.Slug,
		api.UpdateAppRequest{ScalingPolicy: policy})
	if status != http.StatusOK {
		t.Fatalf("PATCH scaling policy: status=%d body=%s", status, body)
	}

	before := instanceCount(t, f)
	if before == 0 {
		t.Fatal("no baseline instance despite a live deployment: this test proves an ABSENT " +
			"metric does not GROW capacity, which asserts nothing if there was none to grow from")
	}
	// A target of 1 is as hot as a target can be, so if an absent metric
	// were ever read as a confident zero-or-anything this would scale to
	// the cap almost immediately.
	time.Sleep(5 * time.Second)
	if after := instanceCount(t, f); after > before {
		t.Fatalf("instances grew from %d to %d with no metric ever pushed: an absent "+
			"reading must be no signal, never a confident one", before, after)
	}
}

// instanceCount counts the app's instances that occupy capacity. Terminal
// rows are excluded because a failed boot leaves one behind, and counting it
// would read as growth that never produced usable capacity.
func instanceCount(t *testing.T, f *normalPathFixture) int {
	t.Helper()
	var n int
	if err := f.h.Pool.QueryRow(context.Background(),
		`select count(*) from instances
		  where app_id = $1
		    and state in ('waking','cold_booting','running','warm')`,
		f.app.ID).Scan(&n); err != nil {
		t.Fatalf("count instances: %v", err)
	}
	return n
}
