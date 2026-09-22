// ram_ceiling_e2e_test.go — invariant §6.2-2, against the real admission path.
//
// CLAUDE.md lists five invariants and says to "enforce with property-based
// tests, never delete". The RAM ceiling is the one the box's survival rests
// on: admit past it and the host OOMs, taking every tenant on it down
// together. It is also the one whose evidence was weakest — the property tests
// live in pkg/sched, in-process, against MemStore and a fake ledger. Nothing
// drove real wakes through apid, schedd and the gateway and then checked what
// the durable rows said.
//
// That distinction is not academic here. #1666 was precisely a case where
// MemStore agreed with the intent and PgStore's SQL did not, and no test ran
// both. An admission ceiling verified only against MemStore is the same bet.
//
// The ceiling is shrunk rather than filled. 47,600 MB is not reachable on a CI
// runner and reaching it is not the point: the interesting behaviour is what
// happens AT the boundary, and compute_nodes.admission_ceiling_mb is the value
// the production path (NodeLedger.Admit) actually enforces against.

package e2e_test

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// ramCeilingApp is one app with its own live deployment, ready to be woken.
type ramCeilingApp struct {
	slug string
	host string
}

// seedRamCeilingApp creates an app through the API and gives it a live
// deployment with a signed layer, so a request to its host performs a real
// wake through the real admission path.
func seedRamCeilingApp(t *testing.T, f *normalPathFixture, slug string) ramCeilingApp {
	t.Helper()
	falsy := false
	body, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug, Type: string(state.AppTypeApp), RequireAuthn: &falsy})
	if status != http.StatusCreated {
		t.Fatalf("create app %s: status=%d body=%s", slug, status, body)
	}
	app, err := f.store.AppBySlug(f.ctx, slug)
	if err != nil {
		t.Fatalf("load app %s: %v", slug, err)
	}
	dep, err := f.store.CreateDeployment(f.ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:" + repeatChar("4", 64),
	})
	if err != nil {
		t.Fatalf("create deployment for %s: %v", slug, err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, dep.ID); err != nil {
		t.Fatalf("mark %s live: %v", slug, err)
	}
	publishNormalPathLayer(t, f, dep.ID)
	return ramCeilingApp{slug: slug, host: slug + ".apps.test.example"}
}

// TestE2E_RAMCeiling_AdmissionNeverExceedsTheNodeCeiling drives more concurrent
// wakes than the node can hold and checks the two things that matter.
//
// The ceiling must HOLD: durable resident RAM never exceeds what the node
// declared, however many requests arrive at once. Admission is the only thing
// standing between a burst of traffic and an OOM that takes out every tenant
// on the box, and it has to hold under concurrency, not just in sequence.
//
// And it must REFUSE, not hang or crash: at least one request has to come back
// with a capacity answer. A ceiling that holds because every wake failed for
// some unrelated reason would satisfy the first check while telling us nothing.
func TestE2E_RAMCeiling_AdmissionNeverExceedsTheNodeCeiling(t *testing.T) {
	f := newNormalPathFixture(t, "ram-ceiling")
	if f == nil {
		return
	}
	faults := e2etest.NewRowFaults(f.h.Pool)

	// Stay inside the plan's app limit. Hobby allows 5 deployed apps and the
	// fixture already created one, so this adds 4 and wakes all 5 — a sixth
	// would be refused with plan_limit_apps before admission ever ran, which
	// is a different gate doing its job and tells us nothing about RAM.
	seeded := []ramCeilingApp{{slug: f.app.Slug, host: f.host}}
	createNormalPathParkedDeployment(t, f)
	for i := range 4 {
		seeded = append(seeded, seedRamCeilingApp(t, f, fmt.Sprintf("ram-ceiling-app-%d", i)))
	}

	// Derive the per-instance cost from what apid actually recorded rather
	// than assuming the plan's number, so a plan-table change cannot silently
	// turn this into a test of nothing.
	app, err := f.store.AppBySlug(f.ctx, seeded[1].slug)
	if err != nil {
		t.Fatalf("load seeded app: %v", err)
	}
	perInstanceMB := app.RAMMB + api.PerVMOverheadMB
	// Room for exactly two instances, nowhere near three.
	ceilingMB := 2*perInstanceMB + (perInstanceMB / 4)
	if err := faults.SetNodeAdmissionCeiling(state.DefaultLocalNodeName, ceilingMB); err != nil {
		t.Fatalf("set admission ceiling: %v", err)
	}
	f.vmmd.SetDefaultVersion("v1")

	// Fire them together. Sequential wakes would let each admission settle and
	// would not exercise the contention the ledger exists to arbitrate.
	var wg sync.WaitGroup
	statuses := make([]int, len(seeded))
	for i, app := range seeded {
		wg.Add(1)
		go func(i int, app ramCeilingApp) {
			defer wg.Done()
			_, _, status := doReqHeaders(t, f.h, app.host, http.MethodGet, "/", nil)
			statuses[i] = status
		}(i, app)
	}
	wg.Wait()

	node, err := f.store.ComputeNodeByName(f.ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("load node: %v", err)
	}

	// The oracle is the durable rows, not the in-memory ledger: asking the
	// ledger whether the ledger is right proves nothing. ComputeNodeUsedMB is
	// the same Σ(ram_mb + PerVMOverheadMB) the placement path reads back.
	assertCeilingHeld := func(when string) {
		t.Helper()
		used, err := f.store.ComputeNodeUsedMB(f.ctx, node.ID)
		if err != nil {
			t.Fatalf("read used mb (%s): %v", when, err)
		}
		if used > int64(ceilingMB) {
			t.Errorf("%s: resident RAM %d MB exceeds the node ceiling %d MB (§6.2-2). "+
				"Admission let through more than the node declared it could hold; in production "+
				"that is the host OOMing and taking every tenant on it with it",
				when, used, ceilingMB)
		}
	}
	assertCeilingHeld("immediately after the burst")

	// The ledger settles asynchronously as wakes finish, so a single reading
	// could miss a transient overshoot. Keep checking.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		assertCeilingHeld("while the burst settles")
		if t.Failed() {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	// Someone must have been told no. Without this, a ceiling that "held"
	// because every single wake failed would pass.
	refused := 0
	served := 0
	for _, s := range statuses {
		switch {
		case s == http.StatusOK:
			served++
		case s == http.StatusTooManyRequests || s == http.StatusServiceUnavailable || s == http.StatusPaymentRequired:
			refused++
		}
	}
	if refused == 0 {
		t.Errorf("no request was refused for capacity (statuses=%v); with %d apps of %d MB "+
			"against a %d MB ceiling, at least one had to be turned away — the cap is not being "+
			"enforced on the request path", statuses, len(seeded), perInstanceMB, ceilingMB)
	}
	if served == 0 {
		t.Errorf("no request was served (statuses=%v); the ceiling held only because nothing "+
			"worked, which proves nothing about admission", statuses)
	}
}
