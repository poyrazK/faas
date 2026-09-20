package conformance

// Conformance cases for the Claim* family.
//
// These are the methods where the two Store implementations are most likely to
// drift, because they are the only ones whose correctness depends on a
// concurrency primitive — and the two implementations use different ones.
// PgStore relies on SQL (`for update skip locked`, conditional UPDATE
// predicates, unique constraints); MemStore holds one process mutex. A mutex
// makes almost any claim look exactly-once, so a MemStore-only test proves
// very little about the path that actually runs in production.
//
// Every case here asserts an ABSOLUTE outcome, per the rule in Run's doc
// comment: "exactly one of two claimers wins", "the older build is skipped in
// favour of the quiet account". An "the two stores agree" assertion would have
// passed while both were wrong, which is how #1666 happened.
//
// The shared property under test is exactly-once: a queued unit of work must
// be handed to one worker, no matter how many ask at once. The failure this
// guards is not a crash — it is a build compiled twice, an alert delivered
// twice, or an app deleted by two concurrent reapers.

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// claimRace runs claim concurrently from n goroutines and returns the
// identifiers that were successfully claimed, in completion order.
//
// It deliberately starts every goroutine from one release barrier. PgStore's
// exactly-once guarantee is enforced by the database under real contention;
// staggered calls would serialize and test nothing.
func claimRace(t *testing.T, n int, claim func() (string, bool, error)) []string {
	t.Helper()
	var (
		mu      sync.Mutex
		claimed []string
		lastErr error
		ready   sync.WaitGroup
		release = make(chan struct{})
		done    sync.WaitGroup
	)
	ready.Add(n)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer done.Done()
			ready.Done()
			<-release
			id, ok, err := claim()
			mu.Lock()
			if ok {
				claimed = append(claimed, id)
			}
			if err != nil {
				lastErr = err
			}
			mu.Unlock()
		}()
	}
	ready.Wait()
	close(release)
	done.Wait()
	// A claim that loses the race returns a benign error; one that fails for
	// any other reason must not be mistaken for a lost race. Surfacing the
	// last error is what turned "0 claimers won" into "the provider column has
	// a closed set and the fixture used a value outside it".
	if len(claimed) == 0 && lastErr != nil {
		t.Fatalf("every claimer failed and none won; last error: %v", lastErr)
	}
	return claimed
}

// distinct reports the number of unique values, so a double-claim shows up as
// len(claimed) != len(distinct(claimed)) rather than as a vague count.
func distinct(values []string) map[string]int {
	counts := map[string]int{}
	for _, v := range values {
		counts[v]++
	}
	return counts
}

// seedQueuedBuild creates a deployment + queued build under fx's app.
func seedQueuedBuild(t *testing.T, fx *Fixture) state.Build {
	t.Helper()
	dep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID:       fx.App.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:" + uuid.NewString(),
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	build, err := fx.Store.CreateBuild(fx.Ctx, dep.ID, state.DeploymentKindTarball, 1024, "")
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}
	return build
}

// testQueuedBuildClaimIsExactlyOnce pins that one queued build is handed to
// exactly one worker even when every worker asks simultaneously.
//
// builderd runs a poller AND a pg_notify consumer, so two claim paths race on
// the same row by design. A double claim here means the same customer build
// runs twice in two microVMs, both writing the same artifact.
func testQueuedBuildClaimIsExactlyOnce(t *testing.T, fx *Fixture) {
	const builds, workers = 3, 8
	for i := 0; i < builds; i++ {
		seedQueuedBuild(t, fx)
	}

	claimed := claimRace(t, workers, func() (string, bool, error) {
		b, err := fx.Store.ClaimNextQueuedBuild(fx.Ctx)
		if err != nil {
			return "", false, err
		}
		return b.ID, true, nil
	})

	if len(claimed) != builds {
		t.Fatalf("claimed %d builds from %d workers, want exactly %d", len(claimed), workers, builds)
	}
	for id, n := range distinct(claimed) {
		if n != 1 {
			t.Errorf("build %s was claimed %d times; a queued build must be claimed exactly once", id, n)
		}
	}

	// The queue is now empty, and an empty queue is ErrNotFound rather than a
	// zero-valued Build — a caller that cannot tell the difference will try to
	// run a build with an empty ID.
	if _, err := fx.Store.ClaimNextQueuedBuild(fx.Ctx); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claim on an empty queue = %v, want ErrNotFound", err)
	}
}

// testTargetedBuildClaimFencesASecondClaimer pins ClaimQueuedBuild's fence:
// claiming a specific build by ID must fail once that build is already running.
func testTargetedBuildClaimFencesASecondClaimer(t *testing.T, fx *Fixture) {
	build := seedQueuedBuild(t, fx)

	claimed := claimRace(t, 6, func() (string, bool, error) {
		b, err := fx.Store.ClaimQueuedBuild(fx.Ctx, build.ID)
		if err != nil {
			return "", false, err
		}
		return b.ID, true, nil
	})

	if len(claimed) != 1 {
		t.Fatalf("%d claimers won the same build, want exactly 1", len(claimed))
	}
	if claimed[0] != build.ID {
		t.Fatalf("claimed build %s, want %s", claimed[0], build.ID)
	}
	if _, err := fx.Store.ClaimQueuedBuild(fx.Ctx, build.ID); err == nil {
		t.Fatal("re-claiming an already-running build succeeded; the claim is not fenced")
	}
}

// testBuildClaimFairnessPrefersTheQuietAccount pins the fairness ordering with
// an absolute expectation rather than "whatever the implementation does".
//
// This is the case that motivated the file. PgStore orders by
// `exists(recent claim), enqueued_at, id` — it DEPRIORITIZES a recently served
// account without excluding it. MemStore builds a skip-set and FILTERS, with a
// starvation fallback. Those are different algorithms that happen to agree on
// the common input, and each store had only its own test.
//
// The contract asserted here is the one both must satisfy: when a quiet
// account has a queued build, it is served before a busy account's OLDER
// build; and when every account is busy, the oldest build is still served
// rather than nothing.
func testBuildClaimFairnessPrefersTheQuietAccount(t *testing.T, fx *Fixture) {
	const window = time.Hour

	busy := seedQueuedBuild(t, fx) // fx.Account, enqueued first
	time.Sleep(2 * time.Millisecond)

	quietAcct, err := fx.Store.CreateAccount(fx.Ctx, "quiet-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	quietApp, err := fx.Store.CreateApp(fx.Ctx, state.App{
		AccountID: quietAcct.ID, Slug: "quiet-" + uuid.NewString(),
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	quietDep, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{
		AppID: quietApp.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	quiet, err := fx.Store.CreateBuild(fx.Ctx, quietDep.ID, state.DeploymentKindTarball, 1024, "")
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}

	// Record a recent claim for the busy account so it is the deprioritized one.
	if err := fx.Store.RecordRecentBuildClaim(fx.Ctx, fx.Account.ID, busy.ID); err != nil {
		t.Fatalf("RecordRecentBuildClaim: %v", err)
	}

	got, err := fx.Store.ClaimNextQueuedBuildWithFairness(fx.Ctx, window)
	if err != nil {
		t.Fatalf("ClaimNextQueuedBuildWithFairness: %v", err)
	}
	if got.ID != quiet.ID {
		t.Fatalf("fairness claim returned %s (busy account's older build), want %s (quiet account)",
			got.ID, quiet.ID)
	}

	// Starvation fallback: the busy account's build is the only one left and
	// must still be served rather than the queue appearing empty.
	got, err = fx.Store.ClaimNextQueuedBuildWithFairness(fx.Ctx, window)
	if err != nil {
		t.Fatalf("fairness claim with only a busy account queued: %v — a busy account must "+
			"be deprioritized, never starved", err)
	}
	if got.ID != busy.ID {
		t.Fatalf("fallback claim returned %s, want %s", got.ID, busy.ID)
	}
}

// testAlertFireClaimIsIdempotentByKey pins ClaimAlertFire's dedupe.
//
// The alert evaluator runs on more than one node; without an idempotency fence
// a single threshold crossing pages the customer once per node.
func testAlertFireClaimIsIdempotentByKey(t *testing.T, fx *Fixture) {
	rule, err := fx.Store.CreateAlertRule(fx.Ctx, state.AlertRule{
		AccountID:           fx.Account.ID,
		AppID:               fx.App.ID,
		Name:                "claim-conformance-" + uuid.NewString(),
		Enabled:             true,
		Metric:              state.AlertMetric("error_rate_pct"),
		Comparison:          state.AlertComparison("gt"),
		Threshold:           5,
		WindowSpec:          state.AlertWindowSpec("5m"),
		Action:              state.AlertAction("webhook"),
		WebhookURL:          "https://example.test/hook",
		WebhookSecretSealed: []byte("sealed-conformance"),
		CooldownMinutes:     15,
		State:               state.AlertState("ok"),
	})
	if err != nil {
		t.Fatalf("CreateAlertRule: %v", err)
	}

	key := "fire-" + uuid.NewString()
	at := time.Now().UTC()

	var (
		mu       sync.Mutex
		wins     int
		firstIDs = map[string]struct{}{}
	)
	claimRace(t, 6, func() (string, bool, error) {
		id, won, err := fx.Store.ClaimAlertFire(fx.Ctx, rule.ID, key, []byte(`{"v":1}`), 9.5, at)
		if err != nil {
			return "", false, err
		}
		mu.Lock()
		if won {
			wins++
		}
		if id != "" {
			firstIDs[id] = struct{}{}
		}
		mu.Unlock()
		return id, won, nil
	})

	if wins != 1 {
		t.Fatalf("%d claimers won the same idempotency key, want exactly 1 — the customer "+
			"is paged once per winner", wins)
	}
	if len(firstIDs) != 1 {
		t.Fatalf("one idempotency key produced %d delivery IDs, want 1", len(firstIDs))
	}

	// A different key on the same rule is a different fire and must win.
	if _, won, err := fx.Store.ClaimAlertFire(fx.Ctx, rule.ID, "fire-"+uuid.NewString(),
		[]byte(`{"v":2}`), 9.9, at.Add(time.Minute)); err != nil || !won {
		t.Fatalf("second distinct fire: won=%v err=%v, want won=true", won, err)
	}
}

// testAppDeletionClaimIsConcurrentlyIdempotent covers the two properties of
// ClaimAppDeletion that testAppDeletionClaim (conformance.go) does not.
//
// That case already pins sequential idempotence, the ErrConflict on RestoreApp
// after a claim, and DeleteAppPermanently's terminal behaviour. Duplicating
// those here would be noise. What it cannot show is behaviour under
// contention, which is exactly where the two stores use different primitives:
// MemStore takes one process mutex, PgStore relies on a conditional UPDATE.
//
// Note this claim is deliberately NOT exclusive — the interface says
// "Repeated claims are idempotent so failed artifact deletion remains
// retryable". The grace sweeper deletes external artifacts after claiming, and
// that deletion can fail partway; a claim that refused the retry would strand
// the app's storage forever. The first draft of this case asserted
// exactly-once and failed against both stores, correctly.
func testAppDeletionClaimIsConcurrentlyIdempotent(t *testing.T, fx *Fixture) {
	app, err := fx.Store.CreateApp(fx.Ctx, state.App{
		AccountID: fx.Account.ID, Slug: "deleting-" + uuid.NewString(),
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := fx.Store.ScheduleAppDeletion(fx.Ctx, app.ID, time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatalf("ScheduleAppDeletion: %v", err)
	}

	const sweepers = 6
	claimed := claimRace(t, sweepers, func() (string, bool, error) {
		if err := fx.Store.ClaimAppDeletion(fx.Ctx, app.ID); err != nil {
			return "", false, err
		}
		return app.ID, true, nil
	})
	if len(claimed) != sweepers {
		t.Fatalf("%d of %d concurrent claims succeeded, want all %d — a claim refused "+
			"under contention strands external artifacts when the sweeper retries",
			len(claimed), sweepers, sweepers)
	}

	// An app still inside its customer restore window is not claimable, however
	// many sweepers ask.
	fresh, err := fx.Store.CreateApp(fx.Ctx, state.App{
		AccountID: fx.Account.ID, Slug: "fresh-" + uuid.NewString(),
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := fx.Store.ScheduleAppDeletion(fx.Ctx, fresh.ID, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("ScheduleAppDeletion: %v", err)
	}
	if err := fx.Store.ClaimAppDeletion(fx.Ctx, fresh.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claim inside the grace window = %v, want ErrNotFound — the customer's "+
			"restore window must survive the sweeper", err)
	}
}

// testWebhookDeliveryClaimFencesReplays pins ClaimWebhookDelivery, the inbound
// provider-webhook dedupe (Stripe/Paddle/GitHub redeliver aggressively).
//
// A second winner means the same provider event is processed twice; on a
// billing webhook that is a duplicate charge or a duplicate plan change.
func testWebhookDeliveryClaimFencesReplays(t *testing.T, fx *Fixture) {
	const provider = "github" // webhook_deliveries_provider_check is a closed set
	deliveryID := "evt-" + uuid.NewString()
	cutoff := time.Now().UTC().Add(-24 * time.Hour)
	expires := time.Now().UTC().Add(time.Hour)

	claimed := claimRace(t, 6, func() (string, bool, error) {
		ok, err := fx.Store.ClaimWebhookDelivery(fx.Ctx, provider, deliveryID, cutoff, expires)
		if err != nil || !ok {
			return "", false, err
		}
		return deliveryID, true, nil
	})

	if len(claimed) != 1 {
		t.Fatalf("%d claimers accepted the same provider delivery, want exactly 1", len(claimed))
	}

	// A redelivery of the same event must still be refused.
	ok, err := fx.Store.ClaimWebhookDelivery(fx.Ctx, provider, deliveryID, cutoff, expires)
	if err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if ok {
		t.Fatal("a replayed provider delivery was claimed a second time")
	}

	// A different event ID is a different delivery and must be accepted.
	ok, err = fx.Store.ClaimWebhookDelivery(fx.Ctx, provider, "evt-"+uuid.NewString(), cutoff, expires)
	if err != nil || !ok {
		t.Fatalf("distinct delivery: ok=%v err=%v, want ok=true", ok, err)
	}
}

// testOperatorIntentClaimIsExactlyOnce pins ClaimPendingOperatorIntent.
func testOperatorIntentClaimIsExactlyOnce(t *testing.T, fx *Fixture) {
	const intents = 2
	for i := 0; i < intents; i++ {
		if _, err := fx.Store.InsertOperatorIntent(fx.Ctx,
			state.OperatorIntentKindForcePark, fx.App.ID, &fx.Account.ID,
			uuid.NewString(), "claim conformance", nil, nil); err != nil {
			t.Fatalf("InsertOperatorIntent: %v", err)
		}
	}

	claimed := claimRace(t, 6, func() (string, bool, error) {
		intent, err := fx.Store.ClaimPendingOperatorIntent(fx.Ctx)
		if err != nil {
			return "", false, err
		}
		return intent.ID, true, nil
	})

	if len(claimed) != intents {
		t.Fatalf("claimed %d intents, want exactly %d", len(claimed), intents)
	}
	for id, n := range distinct(claimed) {
		if n != 1 {
			t.Errorf("operator intent %s claimed %d times, want 1", id, n)
		}
	}
}
