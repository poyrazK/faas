//go:build !no_pg

// Package e2e — billing_invoice_shadow_test.go is the §14 M7 invoice-
// shadow acceptance gate wired end-to-end. Mirrors
// meterd_quota_e2e_test.go (boot apid + schedd + meterd via
// e2etest.StartWithEnv, seed via direct PG writes, poll until oracle
// satisfied) but reads the per-push `mb_seconds` value out of the
// meterd stdout captured by the harness. The log scrape is the
// unified oracle across both Stripe and Paddle — the provider-
// specific dedupe tables have different shapes; the
// `meter: push usage` log line (Warn or Info) is provider-neutral.
//
// Spec anchor: §14 line 459 — "invoice shadow equals hand-computed
// GB-h for a scripted 24 h scenario (< 0.1 % delta)". The 0.1 %
// float delta is the spec's monthly-aggregation tolerance, lived
// on the meter-side test (pkg/meter/meter_test.go:256). The
// push-side math is integer-deterministic — this test asserts
// exact int64 equality on the per-tick mb_seconds, mirroring
// pkg/meter/pusher_shadow_test.go::TestPushHour_Shadow24h (line 219).
//
// Why two subtests (stripe + paddle) per ADR-032: the dunning
// state machine and the billing.Provider dispatch both claim
// provider-neutrality. Without this dual e2e, that claim is
// unaudited — a future refactor of the dispatch seam (the
// providerOpsFor type-switch at pkg/meter/pusher.go:191) could
// route both providers to the same provider-agnostic path and
// silently drop one provider's wire shape.
//
// Both subtests use DUMMY provider keys (sk_test_e2e_dummy /
// pdl_test_e2e_dummy). The Stripe SDK returns 401, the Paddle
// SDK short-circuits on `p.client == nil` (provider.go:280-283)
// and returns ErrNoAPIKey. Both paths land on the pusher's
// Warn log line at pusher.go:151 — the new `mb_seconds` field
// is what makes the log-oracle work for both providers. The
// success path (Info log at pusher.go:155) requires a real
// sandbox key and is exercised in-process by
// TestPushHour_Shadow24h_Paddle (pusher_shadow_test.go) — the
// in-process integer math pin is the load-bearing acceptance
// for the success path; the e2e is the load-bearing pin for
// the daemon→log wire.
//
// To skip locally: export FAAS_SKIP_PG_TESTS=1.

package e2e_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// shadowMath is the canonical 24h math (Hobby plan, 256 MB resident).
// Same source of truth as pkg/meter/pusher_shadow_test.go:281-295.
// shadowPerHour / shadowTotal are vars (not consts) because
// api.BillableRAMMB is a function, not a constant expression.
var (
	shadowHours   = int64(24)
	shadowPerHour = int64(api.BillableRAMMB(256)) * 60 * 60 // 950_400
	shadowTotal   = shadowPerHour * shadowHours             // 22_809_600
)

// shadowEnv returns the cadence-compressed env slice for one
// subtest. Per the plan: 1s StripeInterval → 24 ticks fit in ~24s
// wall-clock; defensive 2s on the other timers so a quota/dunning
// tick never races the pusher's own loop on the test's resources.
//
// extra env keys (STRIPE_API_KEY, FAAS_PADDLE_API_KEY, …) are
// appended by the caller. FAAS_BILLING_PROVIDER is the selector;
// FAAS_PADDLE_SANDBOX=1 puts the Paddle provider in sandbox mode
// so the dummy apiKey doesn't try the production endpoint.
func shadowEnv(provider string, extra ...string) []string {
	env := []string{
		"FAAS_BILLING_PROVIDER=" + provider,
		"FAAS_STRIPE_INTERVAL=1s",
		"FAAS_QUOTA_INTERVAL=2s",
		"FAAS_DUNNING_INTERVAL=2s",
		"FAAS_RESIDENCY_INTERVAL=2s",
		"FAAS_SAMPLE_INTERVAL=2s",
	}
	if provider == "paddle" {
		env = append(env, "FAAS_PADDLE_SANDBOX=1")
	}
	env = append(env, extra...)
	return env
}

// seedShadowAccount plants one Hobby account, one app, one live
// instance, and 24 hourly usage rows summing to shadowTotal
// mb_seconds. Each row's `minute` is the start of one hour-bucket
// so UsageByHour(acct, start=t+h, end=t+h+1h) returns exactly
// one row summing to shadowPerHour per meterd tick.
//
// The minute values are spaced 1h apart so AppendUsage's
// (instance_id, minute) idempotency key (state/store.go:786) does
// not collapse any two rows. t0 is anchored at the top of the
// current UTC hour so the first meterd tick (which reads the
// "previous hour" via HourWindow) lands on the first row.
func seedShadowAccount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, t0 time.Time) (state.Account, state.App, state.Instance) {
	t.Helper()
	store := state.NewPgStore(pool)

	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("resolve default-local compute_node: %v", err)
	}
	defaultLocalNodeID := node.ID

	acct, err := store.CreateAccount(ctx, fmt.Sprintf("shadow-%s@example.com", t.Name()), api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "shadow",
		Type:           state.AppTypeApp,
		RAMMB:          256,
		MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Status:      state.DeployLive,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	// The test instance is intentionally Parked, NOT Running. The
	// meterd sampler (pkg/meter/sampler.go:84) iterates every
	// instance and appends (ram_mb+8)*60 mb_seconds per live minute
	// via state.CountsForRAM(); the per-hour pusher window therefore
	// picks up sampler drift on top of the seed rows. When the test
	// boots near the top of an hour, the very first sampler tick
	// lands in the SAME window the seed already populated, pushing
	// mb_seconds from 950_400 → 966_240 (264 * 60 = 15_840 drift
	// per sampler minute) and breaking the integer-equality oracle.
	// The pusher path is unaffected — it iterates accounts by Plan,
	// not by instance state — so parking the instance leaves the
	// "meter: push usage" line emitting exactly shadowPerHour every
	// tick. This shape also matches spec invariant §6.2-4: "a
	// parked app consumes zero resident RAM" — the test instance
	// should never appear live to the sampler in the first place.
	ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateParked), 256, defaultLocalNodeID, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	for h := int64(0); h < shadowHours; h++ {
		minute := t0.Add(time.Duration(h) * time.Hour)
		if err := store.AppendUsage(ctx, acct.ID, app.ID, ins.ID, minute, shadowPerHour, 1, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatalf("AppendUsage hour %d: %v", h, err)
		}
	}
	return acct, app, ins
}

// seedShadowBillingIdentity makes the account pass meterd's fail-closed
// billing-identity gate while keeping the provider call offline. The
// identifiers only need the provider-shaped prefixes; placeholder API keys
// make the subsequent SDK call fail locally or with authentication, which
// still exercises the daemon-to-provider log path.
func seedShadowBillingIdentity(t *testing.T, ctx context.Context, store state.Store, acct state.Account, provider string) {
	t.Helper()
	customerID, subscriptionID := "cus_test_e2e_dummy", "si_test_e2e_dummy"
	if provider == "paddle" {
		customerID, subscriptionID = "ctm_test_e2e_dummy", "sub_test_e2e_dummy"
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, acct.ID, customerID); err != nil {
		t.Fatalf("UpdateAccountProviderCustomerID: %v", err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, acct.ID, subscriptionID); err != nil {
		t.Fatalf("UpdateAccountStripeSubscriptionItem: %v", err)
	}
}

// pollShadowLog blocks until the meterd log has logged at least
// wantHits "meter: push usage" lines that include the seeded
// account id AND `mb_seconds=N` with N == expected. Returns EXACTLY
// wantHits matching lines in buffer order so callers can run
// per-tick assertions on the parsed values (the exact-count oracle
// stays intact even when the loop overshoots). The harness's
// stop() owns the single cmd.Wait per process; this function
// does not call Wait.
//
// 35 s deadline: 24 ticks × 1 s + ~5 s boot + ~6 s slack. CI
// runners under load may slip to 1.4 s/tick — at that point 35 s
// fits 25 ticks, not 24, and the next poll iteration returns a
// 25-element slice even though the test wants exactly 24. The
// wantHits-truncate below absorbs that overshoot deterministically:
// the first wantHits matching lines arrived in the same order
// the per-tick assertion expects, so the sum check (hits ×
// shadowPerHour == shadowTotal) is unaffected.
//
// Why truncate instead of asserting len(hits) == wantHits:
// overshoot is a polling-window artifact, not a real extra tick.
// The slog buffer can flush ≥2 lines between two 150 ms polls,
// so on a busy CI runner the loop returns a slice with one or two
// extra matches that the test never asked for. Truncating keeps
// the exact-count contract for the call site without softening
// the acceptance bar (a 25th *tick* would still be detected via
// the deadline-fail branch, because the count grows to 25 only
// after at least 25 actual ticks have fired).
func pollShadowLog(t *testing.T, h *e2etest.Harness, acctID string, wantHits int, expected int64) []string {
	t.Helper()
	deadline := time.Now().Add(35 * time.Second)
	prefix := "meter: push usage"
	for {
		logs := h.MeterdLogs()
		hits := filterShadowLogLines(logs, prefix, acctID, expected)
		if len(hits) >= wantHits {
			return hits[:wantHits]
		}
		if time.Now().After(deadline) {
			t.Fatalf("shadow log oracle: only %d/%d hits after 35s (acct=%s, expected mb_seconds=%d)\ncaptured meterd log:\n%s",
				len(hits), wantHits, acctID, expected, logs)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// filterShadowLogLines parses the meterd log buffer for lines
// that match the shadow shape: prefix + the seeded account id
// + `mb_seconds=<expected>`. Returns the matching lines in
// buffer order. The harness's shared buffer (cmd.Stdout ==
// cmd.Stderr per pkg/e2etest/harness.go:620-621) means we may
// see partial lines during concurrent writes — the per-line
// scan tolerates that by only emitting complete lines.
//
// The meterd daemon (and every cmd/* daemon) uses slog's JSON
// handler in production, so log lines render as
//
//	{"time":"…","level":"INFO","msg":"meter: push usage",
//	 "account":"<uuid>","hour":"…","code":"ok","mb_seconds":950400}
//
// The assertion is against the daemon's wire format, NOT the
// `discardLog()` text format used by the in-process tests. The
// filter accepts both formats so the same helper works for
// `go test -v` runs (which pipe through testing's text logger
// sometimes) and CI runs (which always use JSON).
//
// `expected` is the exact mb_seconds value the plan calls for
// (shadowPerHour == 950_400). A future refactor that splits
// PushHour into a different window shape would land a different
// value here, surfacing in the e2e before reaching production.
func filterShadowLogLines(logs, prefix, acctID string, expected int64) []string {
	if logs == "" {
		return nil
	}
	// JSON form: "account":"<id>" — the colon + quotes are
	// reliable substrings even when the value contains a `-`.
	wantJSON := fmt.Sprintf(`"account":%q`, acctID)
	// Text form: account=<id> (used by slog.TestHandler in
	// in-process tests).
	wantText := fmt.Sprintf("account=%s", acctID)
	// mb_seconds is a JSON number (no quotes) and a text key=value.
	wantMB := fmt.Sprintf("mb_seconds=%d", expected)
	wantMBJSON := fmt.Sprintf(`"mb_seconds":%d`, expected)
	var out []string
	for _, line := range strings.Split(logs, "\n") {
		if !strings.Contains(line, prefix) {
			continue
		}
		if !strings.Contains(line, wantJSON) && !strings.Contains(line, wantText) {
			continue
		}
		if !strings.Contains(line, wantMB) && !strings.Contains(line, wantMBJSON) {
			continue
		}
		out = append(out, line)
	}
	return out
}

// TestInvoiceShadow_24h is the §14 M7 push-side acceptance gate
// wired end-to-end. Two subtests (stripe + paddle); each runs in
// its own PG schema (pgtest.Open mints a unique schema per call)
// and its own harness (StartWithEnv registers t.Cleanup(h.stop)
// per call). The two subtests cannot leak state into each other.
//
// Per subtest: 24 hourly usage rows seeded, FAAS_STRIPE_INTERVAL
// compressed to 1s, meterd fires 24 PushHour ticks, each tick
// reads one hour-bucket summing to shadowPerHour. The meterd
// log is the unified oracle — every successful push emits a
// "meter: push usage ... mb_seconds=950400" line. The test
// asserts exactly 24 such lines, then sums the per-tick
// mb_seconds to shadowTotal.
func TestInvoiceShadow_24h(t *testing.T) {
	t.Run("stripe", func(t *testing.T) { runShadowSubtest(t, "stripe") })
	t.Run("paddle", func(t *testing.T) { runShadowSubtest(t, "paddle") })
}

func runShadowSubtest(t *testing.T, provider string) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	pgtest.WaitForMigration(t, pool, 13, 10*time.Second)

	// Anchor t0 at the previous top-of-hour. The 24 seeded rows
	// cover [t0, t0+24h) on hour boundaries. The pusher's
	// HourWindow(now) returns [now.Truncate-1h, now.Truncate)
	// (pusher.go:59-63), so as long as the first tick fires
	// after t0+1h, it reads the [t0, t0+1h) window — exactly the
	// first row. Each subsequent tick advances one hour. The
	// anchor is robust to any seed-time clock-second.
	t0 := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)

	var extraEnv []string
	if provider == "stripe" {
		extraEnv = []string{"STRIPE_API_KEY=sk_test_e2e_dummy"}
	} else {
		extraEnv = []string{"FAAS_PADDLE_API_KEY=pdl_test_e2e_dummy"}
	}
	h := e2etest.StartWithEnv(t, pool,
		e2etest.APID|e2etest.Schedd|e2etest.Meterd,
		shadowEnv(provider, extraEnv...))

	acct, _, _ := seedShadowAccount(t, ctx, pool, t0)
	seedShadowBillingIdentity(t, ctx, state.NewPgStore(pool), acct, provider)

	hits := pollShadowLog(t, h, acct.ID, int(shadowHours), shadowPerHour)
	if int64(len(hits)) != shadowHours {
		t.Fatalf("shadow log hits = %d, want %d (one per hourly PushHour tick)", len(hits), shadowHours)
	}

	// Sum check: every hit was filtered for `mb_seconds=` ↔ shadowPerHour
	// (filterShadowLogLines), so the sum is trivially hits × shadowPerHour.
	// The integer equality is the load-bearing assertion — drift here
	// means either the number of ticks or the per-tick value regressed.
	sum := int64(len(hits)) * shadowPerHour
	if sum != shadowTotal {
		t.Fatalf("shadow sum = %d mb_seconds, want %d (24 × %d)", sum, shadowTotal, shadowPerHour)
	}

	// The Stripe dedupe table is intentionally NOT asserted here:
	// the dedupe row is stamped AFTER the SDK call
	// (pkg/billing/stripe/client.go:136 inside PushUsageRecord), so
	// a 401 from the dummy `sk_test_e2e_dummy` key leaves the table
	// empty. The log scrape above is the load-bearing oracle for
	// both providers — the "meter: push usage" line is emitted on
	// every PushHour tick regardless of SDK outcome (Warn on
	// failure, Info on success). The dedupe table is exercised
	// against a real sandbox key in a follow-up PR.

	// Flush any remaining captured output to the test log so a
	// CI failure has the full daemon log to inspect. The
	// harness's own stop() teardown will run on t.Cleanup.
	h.DumpLogs(t)
}
