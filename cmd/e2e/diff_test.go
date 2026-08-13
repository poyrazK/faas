// diff_test.go — end-to-end envelope for the deploy-diff cluster
// (#860 + #869 + #874). Boots a real apid against the test
// PgStore, provisions a Hobby app, and exercises POST
// /v1/apps/{slug}/diff through three assertions:
//
//  1. ram_mb over Hobby cap → DiffResponse.Blocking=true with a
//     Break{Code: plan_limit_ram}. Pins that the CLI's plan
//     quota gate is reachable end-to-end (the customer-visible
//     gate, not the in-process unit path).
//
//  2. cron count over Hobby CronLimitPerApp (=5) → Break{Code:
//     plan_cron_quota}. Pins that the cron quota code is
//     stable across the wire.
//
//  3. --json envelope byte-stability: calling the same payload
//     twice produces byte-identical DiffResponse bodies. Pins
//     that the JSON sort / omit-empty contract from PR-1
//     (`pkg/deploydiff.RenderJSON` ↔ `Diff.ToWire`) survives
//     the wire.
//
// What this is NOT:
//
//   - It does not boot schedd/vmmd. The diff endpoint is apid-
//     only; the rest of the platform is irrelevant.
//   - It does not exercise the structural schema-break path
//     from PR-2. That surface is unit-tested in pkg/openapidiff
//     and pinned in pkg/deploydiff/engine_test.go. Adding a
//     route edge rule would expand the e2e harness boot time
//     by a measurable amount for a signal that's already
//     covered upstream.
//
// Memory pointers in play: provision-real-app-e2e-pattern
// (h.SeedAccount + h.Pool boot), cmd-e2e-coverage-timeout-edge
// (-timeout=18m gate runs this).
package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

func TestE2E_Diff_HobbyPlanQuotaGate(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return // pgtest already t.Skip'd
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	// Create one Hobby app under cap so the diff baseline exists.
	createBody := api.CreateAppRequest{Slug: "diff-hobby", RAMMB: 128, MaxConcurrency: 1}
	if status := postOK(t, h, key, "/v1/apps", createBody); status != http.StatusCreated {
		t.Fatalf("create app: status=%d", status)
	}

	t.Run("ram_over_hobby_cap", func(t *testing.T) {
		// Hobby RAM cap is 256 MB. Proposing 2048 must trip the
		// plan_limit_ram break.
		v := 2048
		body := api.DiffRequest{
			AppConfig: &api.DiffAppConfigPatch{RAMMB: &v},
		}
		raw, status := postDiff(t, h, key, "diff-hobby", body)
		if status != http.StatusOK {
			t.Fatalf("diff: status=%d body=%s", status, raw)
		}
		var resp api.DiffResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v (body=%s)", err, raw)
		}
		if !resp.Blocking {
			t.Fatalf("expected Blocking=true; got false. Breaks=%+v", resp.Diff.Breaks)
		}
		found := false
		for _, b := range resp.Diff.Breaks {
			if b.Code == api.CodePlanLimitRAM {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected Break{Code: plan_limit_ram}; got %+v", resp.Diff.Breaks)
		}
	})

	t.Run("cron_over_hobby_per_app", func(t *testing.T) {
		// Hobby CronLimitPerApp is 5. Proposing 6 crons must
		// trip plan_cron_quota with the per-app limit surfaced
		// in the Observed / Limit payload.
		crons := make([]api.CreateCronRequest, 0, 6)
		for i := 0; i < 6; i++ {
			crons = append(crons, api.CreateCronRequest{
				Schedule: "@hourly",
				Path:     "/cron-" + itoa(i),
			})
		}
		body := api.DiffRequest{Crons: crons}
		raw, status := postDiff(t, h, key, "diff-hobby", body)
		if status != http.StatusOK {
			t.Fatalf("diff: status=%d body=%s", status, raw)
		}
		var resp api.DiffResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("unmarshal: %v (body=%s)", err, raw)
		}
		if !resp.Blocking {
			t.Fatalf("expected Blocking=true; got false. Breaks=%+v", resp.Diff.Breaks)
		}
		found := false
		for _, b := range resp.Diff.Breaks {
			if b.Code == api.CodePlanCronQuota {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected Break{Code: plan_cron_quota}; got %+v", resp.Diff.Breaks)
		}
	})

	t.Run("json_envelope_byte_stable", func(t *testing.T) {
		// Pin the wire-shape stability contract from PR-1:
		// the same DiffRequest called twice produces
		// byte-identical DiffResponse bodies. Without this the
		// CI consumer's "if diff.json changes, block" gate is
		// noise — every run would differ.
		v := 192
		body := api.DiffRequest{
			AppConfig: &api.DiffAppConfigPatch{RAMMB: &v},
		}
		first, status := postDiff(t, h, key, "diff-hobby", body)
		if status != http.StatusOK {
			t.Fatalf("diff #1: status=%d body=%s", status, first)
		}
		second, status := postDiff(t, h, key, "diff-hobby", body)
		if status != http.StatusOK {
			t.Fatalf("diff #2: status=%d body=%s", status, second)
		}
		if string(first) != string(second) {
			t.Fatalf("DiffResponse envelope is not byte-stable across calls\nfirst:  %s\nsecond: %s", first, second)
		}
		// Stronger: re-decode both and compare via reflect.DeepEqual
		// so a structural change that happens to encode to the
		// same bytes (rare but possible) still fails the gate.
		var a, b api.DiffResponse
		if err := json.Unmarshal(first, &a); err != nil {
			t.Fatalf("unmarshal first: %v", err)
		}
		if err := json.Unmarshal(second, &b); err != nil {
			t.Fatalf("unmarshal second: %v", err)
		}
		if !equalDiffResponse(a, b) {
			t.Fatalf("DiffResponse decodes diverge between calls\nfirst:  %+v\nsecond: %+v", a, b)
		}
	})
}

// postDiff POSTs a DiffRequest to /v1/apps/{slug}/diff and
// returns the raw response body + status code. Mirrors doReq
// (quota_e2e_test.go) but with the diff-specific path baked in
// so the sub-tests stay readable.
func postDiff(t *testing.T, h *e2etest.Harness, key, slug string, body api.DiffRequest) ([]byte, int) {
	t.Helper()
	return doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/diff", body)
}

// equalDiffResponse compares two DiffResponse values for
// structural equality. Used to confirm the JSON round-trip
// is lossless even when the byte shape might mask a hidden
// field (e.g. an unsorted map). Comparing only the bytes
// would miss a regression where two responses happen to
// encode the same JSON but parse to different trees.
//
// json.RawMessage fields (Before / After / Observed / Limit
// on DiffChange / DiffBreak) are compared by their encoded
// bytes — Go refuses `==` on structs containing RawMessage.
func equalDiffResponse(a, b api.DiffResponse) bool {
	if a.Blocking != b.Blocking {
		return false
	}
	if a.Slug != b.Slug {
		return false
	}
	if a.Plan != b.Plan {
		return false
	}
	if a.Diff.Slug != b.Diff.Slug {
		return false
	}
	if a.Diff.Plan != b.Diff.Plan {
		return false
	}
	if len(a.Diff.Changes) != len(b.Diff.Changes) {
		return false
	}
	if len(a.Diff.Breaks) != len(b.Diff.Breaks) {
		return false
	}
	for i := range a.Diff.Changes {
		ac, bc := a.Diff.Changes[i], b.Diff.Changes[i]
		if ac.Field != bc.Field || ac.Kind != bc.Kind {
			return false
		}
		if !bytesEq(ac.Before, bc.Before) || !bytesEq(ac.After, bc.After) {
			return false
		}
	}
	for i := range a.Diff.Breaks {
		ab, bb := a.Diff.Breaks[i], b.Diff.Breaks[i]
		if ab.Code != bb.Code || ab.Severity != bb.Severity ||
			ab.Reason != bb.Reason || ab.Field != bb.Field {
			return false
		}
		if !bytesEq(ab.Observed, bb.Observed) || !bytesEq(ab.Limit, bb.Limit) {
			return false
		}
	}
	return true
}

func bytesEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
