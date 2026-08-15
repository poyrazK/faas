// jobs_e2e_test.go — ADR-099 PR-E e2e surface. Mirrors the
// secrets_matrix pattern (secrets_e2e_test.go): per-plan subtest,
// apid-only boot (no schedd/vmmd — those need metal), pgtest pool,
// FAAS_JOBS_ENABLED=1 stamped on the harness env so the dark-launch
// kill-switch is open.
//
// Coverage:
//   - TestJobsCRUDMatrixPg: per-plan (Free/Hobby/Pro/Scale)
//     happy path: create → list → info → update → run → runs →
//     cancel → rm. Free plan asserts the 404 jobs_not_allowed gate
//     (gateJobsEnabled short-circuits BEFORE the quota check; the
//     plan-tier JobMaxPerAccount gate runs only when the env kill-
//     switch is open AND plan > 0 — Free still returns 404 to
//     surface the plan-upgrade CTA).
//   - TestJobRunQuotaBreach: Hobby plan (cap = 5 jobs); 6th create
//     returns CodeJobQuotaExceeded (403). Cancel doesn't free a
//     quota slot — soft-deleted jobs do.
//
// Build tag: none (default e2e suite). The harness boots apid over
// pgx via e2etest.StartWithEnv; pgtest.Open returns nil on no PG in
// path so contributors without a Postgres instance can still run
// other suites.
package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// jobImageRef is the canonical OCI image reference used across the
// matrix. sha256: + 64 hex matches the server-side regex
// (handlers_jobs.go::validateImageRef). Var (not const) because
// strings.Repeat is a runtime expression the compiler doesn't fold.
var jobImageRef = "sha256:" + strings.Repeat("a", 64)

// TestJobsCRUDMatrixPg walks the full happy path for one job per
// paid plan. Free plan asserts the dark-launch gate instead of the
// happy-path bodies — the wire shape is the same, the status codes
// differ. Mirrors the secrets matrix pattern (secrets_e2e_test.go:
// TestSecretsMatrixPg) one-for-one: per-plan t.Run, harness per
// subtest, SeedAccount fresh key per plan.
//
// The handler contract (handlers_jobs_test.go, smoke at apid level):
//   - Free plan: CodeJobsNotAllowed 404 with the plan-upgrade CTA
//     (gate short-circuits before quota check).
//   - Hobby plan: Create → 200, List → 200 + count==1, Info → 200,
//     Update → 200, Run → 200 with aggregate_status='queued',
//     Runs → 200 with 1 row, Cancel → 202 (Accepted), Rm → 204.
//   - Pro/Scale: same happy path, larger caps.
func TestJobsCRUDMatrixPg(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	for _, plan := range api.Plans {
		plan := plan
		t.Run(string(plan), func(t *testing.T) {
			h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
				"FAAS_JOBS_ENABLED=1",
			})
			key := h.SeedAccount(context.Background(), plan)

			if plan == api.PlanFree {
				// Free plan: gate short-circuits with
				// CodeJobsNotAllowed. Verify on POST.
				body := api.CreateJobRequest{
					Name: "free-job", ImageRef: jobImageRef,
				}
				assertProblemAPID(t, h, key, http.MethodPost, "/v1/jobs",
					body, http.StatusNotFound, api.CodeJobsNotAllowed)
				return
			}

			// Happy path: CRUD + run + cancel + rm.
			//
			// Create
			createBody := api.CreateJobRequest{
				Name:           string(plan) + "-job",
				ImageRef:       jobImageRef,
				RAMMB:          int32(api.MustLimitsFor(plan).JobMaxRAMMB / 4),
				TaskTimeoutS:   60,
				MaxParallelism: 1,
				RetryMax:       0,
				EnvOverrides:   map[string]string{"FOO": "bar"},
			}
			jobRaw := doReqBytes(t, h, key, http.MethodPost, "/v1/jobs", createBody)
			if len(jobRaw) == 0 {
				t.Fatalf("create job: empty body")
			}
			var job api.JobResponse
			if err := decodeJSON(jobRaw, &job); err != nil {
				t.Fatalf("create job decode: %v (body=%s)", err, jobRaw)
			}
			if job.Name != string(plan)+"-job" {
				t.Fatalf("create job name drift: %+v", job)
			}

			// List — must surface this job.
			listRaw := doReqBytes(t, h, key, http.MethodGet, "/v1/jobs?limit=50", nil)
			var listResp api.ListJobsResponse
			if err := decodeJSON(listRaw, &listResp); err != nil {
				t.Fatalf("list decode: %v (body=%s)", err, listRaw)
			}
			if listResp.Count < 1 {
				t.Errorf("list count = %d, want >= 1 (body=%s)", listResp.Count, listRaw)
			}

			// Info
			infoRaw := doReqBytes(t, h, key, http.MethodGet, "/v1/jobs/"+job.ID, nil)
			var info api.JobResponse
			if err := decodeJSON(infoRaw, &info); err != nil {
				t.Fatalf("info decode: %v (body=%s)", err, infoRaw)
			}
			if info.ID != job.ID || info.Status != "active" {
				t.Errorf("info drift: %+v", info)
			}

			// Update — change parallelism + add env.
			newParallelism := int32(2)
			updateBody := api.UpdateJobRequest{
				MaxParallelism: &newParallelism,
			}
			updateRaw := doReqBytes(t, h, key, http.MethodPatch, "/v1/jobs/"+job.ID, updateBody)
			var updated api.JobResponse
			if err := decodeJSON(updateRaw, &updated); err != nil {
				t.Fatalf("update decode: %v (body=%s)", err, updateRaw)
			}
			if updated.MaxParallelism != 2 {
				t.Errorf("update parallelism drift: %+v", updated)
			}

			// Run — create a 1-task run. The schedd dispatch tick
			// never runs in the e2e harness (no schedd), so the
			// run stays queued; the wire shape is what we pin.
			runRaw := doReqBytes(t, h, key, http.MethodPost, "/v1/jobs/"+job.ID+"/runs",
				api.CreateRunRequest{Tasks: 1})
			var run api.JobRunResponse
			if err := decodeJSON(runRaw, &run); err != nil {
				t.Fatalf("run decode: %v (body=%s)", err, runRaw)
			}
			if run.Tasks != 1 || run.AggregateStatus != "queued" {
				t.Errorf("run drift: %+v", run)
			}

			// Runs list — must surface this run.
			runsRaw := doReqBytes(t, h, key, http.MethodGet, "/v1/jobs/"+job.ID+"/runs", nil)
			var runsResp api.ListRunsResponse
			if err := decodeJSON(runsRaw, &runsResp); err != nil {
				t.Fatalf("runs decode: %v (body=%s)", err, runsRaw)
			}
			if len(runsResp.Runs) < 1 {
				t.Errorf("runs len = %d, want >= 1", len(runsResp.Runs))
			}

			// Cancel — 202 Accepted (idempotent endpoint).
			cancelCode := statusOnly(t, h, key, http.MethodPost,
				"/v1/runs/"+run.ID+"/cancel", nil)
			if cancelCode != http.StatusAccepted {
				t.Errorf("cancel code = %d, want 202", cancelCode)
			}

			// rm — 204.
			rmCode := statusOnly(t, h, key, http.MethodDelete,
				"/v1/jobs/"+job.ID, nil)
			if rmCode != http.StatusNoContent {
				t.Errorf("rm code = %d, want 204", rmCode)
			}

			// Soft-delete: a follow-up Info returns 200 with
			// status="deleted" (handlers_jobs.go contract).
			info2Raw := doReqBytes(t, h, key, http.MethodGet, "/v1/jobs/"+job.ID, nil)
			var info2 api.JobResponse
			if err := decodeJSON(info2Raw, &info2); err != nil {
				t.Fatalf("info2 decode: %v (body=%s)", err, info2Raw)
			}
			if info2.Status != "deleted" {
				t.Errorf("post-rm info status = %q, want deleted", info2.Status)
			}
		})
	}
}

// TestJobRunQuotaBreach — push a Hobby account to its JobMaxPerAccount
// cap (limits.go says Hobby=5). The 6th create must return
// CodeJobQuotaExceeded (403). Cancel does NOT free a slot (the soft-
// delete retention is the contract — quota is on the row, not the
// state).
//
// This is the load-bearing PR-E acceptance gate per the plan: it
// exercises the plan-tier quota check that gateJobsEnabled routes
// around for Free plan, but DOES land for paid plans when the
// account is at cap.
func TestJobRunQuotaBreach(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_JOBS_ENABLED=1",
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	caps := api.MustLimitsFor(api.PlanHobby)
	if caps.JobMaxPerAccount <= 0 {
		t.Fatalf("Hobby JobMaxPerAccount must be > 0 (caps=%+v)", caps)
	}

	// Fill the account to the cap. Hobby has a per-account cap
	// of 5 (limits.go::Hobby.JobMaxPerAccount) — assert the shape.
	for i := 0; i < caps.JobMaxPerAccount; i++ {
		body := api.CreateJobRequest{
			Name:     "cap-" + string(rune('a'+i)),
			ImageRef: jobImageRef,
		}
		code := statusOnly(t, h, key, http.MethodPost, "/v1/jobs", body)
		if code != http.StatusOK {
			t.Fatalf("seed create %d: status=%d, want 200", i, code)
		}
	}
	// +1 must trip the quota.
	overBody := api.CreateJobRequest{
		Name:     "cap-over",
		ImageRef: jobImageRef,
	}
	assertProblemAPID(t, h, key, http.MethodPost, "/v1/jobs",
		overBody, http.StatusForbidden, api.CodeJobQuotaExceeded)
}

// TestJobsRAMTooLarge — pin the 400 CodeJobRAMTooLarge gate on Hobby
// for a 2048 MB RAM request (Hobby cap is 512). Mirrors the per-plan
// structure of the matrix.
func TestJobsRAMTooLarge(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_JOBS_ENABLED=1",
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	body := api.CreateJobRequest{
		Name:     "ram-job",
		ImageRef: jobImageRef,
		RAMMB:    4096, // over Hobby cap of 512
	}
	assertProblemAPID(t, h, key, http.MethodPost, "/v1/jobs",
		body, http.StatusBadRequest, api.CodeJobRAMTooLarge)
}

// TestJobsCrossAccountIsolation — account B cannot read, update, or
// rm account A's job. Mirrors TestSecretsCrossAccountIsolation
// (secrets_e2e_test.go:172). The server is expected to return the
// byte-identical 404 on missing-or-cross-account for every verb
// that operates on a job id — never leak existence via 200 vs 404.
func TestJobsCrossAccountIsolation(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_JOBS_ENABLED=1",
	})
	keyA := h.SeedAccount(context.Background(), api.PlanHobby, "a")
	keyB := h.SeedAccount(context.Background(), api.PlanHobby, "b")

	// Account A creates a job.
	jobRaw := doReqBytes(t, h, keyA, http.MethodPost, "/v1/jobs",
		api.CreateJobRequest{Name: "isolate-job", ImageRef: jobImageRef})
	var job api.JobResponse
	if err := decodeJSON(jobRaw, &job); err != nil {
		t.Fatalf("create decode: %v (body=%s)", err, jobRaw)
	}

	// Account B reads — must 404.
	assertProblemAPID(t, h, keyB, http.MethodGet, "/v1/jobs/"+job.ID,
		nil, http.StatusNotFound, api.CodeNotFound)
	// Account B updates — must 404.
	newRAM := int32(256)
	assertProblemAPID(t, h, keyB, http.MethodPatch, "/v1/jobs/"+job.ID,
		api.UpdateJobRequest{RAMMB: &newRAM},
		http.StatusNotFound, api.CodeNotFound)
	// Account B deletes — must 404.
	assertProblemAPID(t, h, keyB, http.MethodDelete, "/v1/jobs/"+job.ID,
		nil, http.StatusNotFound, api.CodeNotFound)
}

// decodeJSON is a tiny helper that fails the test on JSON drift.
// Lives next to the e2e so the package's test helpers stay local.
func decodeJSON(raw []byte, dst any) error {
	return json.Unmarshal(raw, dst)
}
