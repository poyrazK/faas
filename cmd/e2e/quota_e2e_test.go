// quota_e2e_test.go — M5 acceptance: plan quotas enforced against the real
// PgStore through a real apid subprocess.
//
// This is the cross-process counterpart to cmd/apid/server_test.go's
// in-package TestQuotaMatrix. Same matrix, same RFC 7807 codes; the path
// under test is HTTP → apid handler → PgStore → CHECK constraint, instead of
// HTTP → handler → MemStore. Catches drift between the two surfaces before
// it can ship — the kind of drift where MemStore accepts something PgStore
// rejects, or vice versa.
//
// Build tag: (none). CI-safe. Runs under `make test`. Requires Postgres (skip
// via FAAS_SKIP_PG_TESTS) and a buildable ./cmd/apid.

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// TestQuotaMatrixPg is the M5 acceptance gate for "quotas enforced (plan
// matrix table-test)". Per plan (free/hobby/pro/scale) it exercises:
//
//   - ram-over-cap       → 403 + CodePlanLimitRAM
//   - concurrency-over-cap → 403 + CodePlanLimitConcur
//   - app-count-over-cap   → 403 + CodePlanLimitApps
//
// against the real PgStore + migrations + apid HTTP handler. No KVM, no
// FC, no guest — this half of §14 M5 is purely a control-plane test.
func TestQuotaMatrixPg(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return // pgtest already t.Skip'd
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// One harness per plan. Booting apid per subtest is cheap (sub-second,
	// build cache hit) and isolates subtest state.
	for _, plan := range api.Plans {
		limits := api.MustLimitsFor(plan)
		plan := plan // pin
		t.Run(string(plan), func(t *testing.T) {
			h := e2etest.Start(t, pool, e2etest.APID)
			key := h.SeedAccount(context.Background(), plan)

			t.Run("ram-over-cap", func(t *testing.T) {
				body := api.CreateAppRequest{Slug: "ram-app", RAMMB: limits.RAMMB + 1}
				assertProblem(t, h, key, http.MethodPost, "/v1/apps", body, http.StatusForbidden, api.CodePlanLimitRAM)
			})
			t.Run("concurrency-over-cap", func(t *testing.T) {
				body := api.CreateAppRequest{Slug: "cc-app", MaxConcurrency: limits.MaxConcurrency + 1}
				assertProblem(t, h, key, http.MethodPost, "/v1/apps", body, http.StatusForbidden, api.CodePlanLimitConcur)
			})
			t.Run("app-count-over-cap", func(t *testing.T) {
				for i := 0; i < limits.DeployedApps; i++ {
					body := api.CreateAppRequest{Slug: fmt.Sprintf("%s-app-%d", plan, i)}
					got := postOK(t, h, key, "/v1/apps", body)
					if got != http.StatusCreated {
						t.Fatalf("app %d under cap: status=%d", i, got)
					}
				}
				body := api.CreateAppRequest{Slug: string(plan) + "-over"}
				assertProblem(t, h, key, http.MethodPost, "/v1/apps", body, http.StatusForbidden, api.CodePlanLimitApps)
			})
		})
	}
}

// --- helpers ---------------------------------------------------------------

// assertProblem POSTs body and asserts the response is a problem document
// with the expected status + code. Logs the body on mismatch.
func assertProblem(t *testing.T, h *e2etest.Harness, key, method, path string, body any, wantStatus int, wantCode string) {
	t.Helper()
	raw, status := doReq(t, h, key, method, path, body)
	if status != wantStatus {
		t.Fatalf("status=%d want %d: %s", status, wantStatus, raw)
	}
	var p api.Problem
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode problem: %v (body=%s)", err, raw)
	}
	if p.Code != wantCode {
		t.Fatalf("code=%q want %q (body=%s)", p.Code, wantCode, raw)
	}
}

// postOK POSTs body and returns the status code. Used for the under-cap loop.
func postOK(t *testing.T, h *e2etest.Harness, key, path string, body any) int {
	t.Helper()
	_, status := doReq(t, h, key, http.MethodPost, path, body)
	return status
}

// doReq issues a JSON request and returns (body bytes, status).
func doReq(t *testing.T, h *e2etest.Harness, key, method, path string, body any) ([]byte, int) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, h.APIDURL+path, r)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode
}

// dbMigrateUp runs the goose migrations against the test pool. The daemon
// subprocess also calls MigrateUp on startup, but doing it here makes a
// failure surface in the test log with our context rather than as a silent
// daemon crash.
func dbMigrateUp(t *testing.T, pool *pgxpool.Pool) error {
	t.Helper()
	return db.MigrateUp(context.Background(), pool)
}
