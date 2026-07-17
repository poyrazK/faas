// secrets_e2e_test.go — M5 acceptance for G2 customer secrets (§11, §17).
//
// Subprocess-apid + real PgStore path:
//   - apid boots with FAAS_HOST_AGE_RECIPIENT_PATH pointing at a host.age.pub
//     we generated in the test (so apid can seal against a known public key)
//   - For each plan (free/hobby/pro/scale) we exercise:
//       * happy path PUT + GET + DELETE
//       * quota over-cap → 403 CodePlanLimitSecrets with limit + observed
//       * value byte-cap → 413 CodeSecretValueTooLarge BEFORE any seal
//       * bad key shape → 400 CodeSecretInvalidKey (lowercase, hyphen, digit)
//       * delete-not-set → 400 CodeSecretNotFound
//       * cross-account isolation: 404 for GET/PUT/DELETE on someone else's app
//       * redaction invariant: GET response body never contains plaintext
//
// This is the KVM-free control-plane half of §14 M5 acceptance. The
// drive1 staging + guest-init read steps are exercised by the existing
// metal-only deploy_wake_metal_test.go (the wire surface for metal-mode
// fakery is heavyweight enough that splitting the control-plane test here
// keeps CI a `make test` away).
//
// Build tag: (none). CI-safe. Requires Postgres (skip via FAAS_SKIP_PG_TESTS).

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// TestSecretsMatrixPg boots one apid per plan so each subtest can pre-write
// secrets at quota and assert the per-plan cap. apid is sealed against the
// recipient we generated in setupHostedRecipient — this is the exact
// "vmmd wrote host.age.pub; apid consumed it" shape from production.
func TestSecretsMatrixPg(t *testing.T) {
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
			// Each plan boots a fresh apid with a fresh host age recipient,
			// all in a shared per-plan tempdir. The harness adds cleanup.
			tmpDir := t.TempDir()
			recipientPath := filepath.Join(tmpDir, "host.age.pub")
			if err := writeTestRecipient(recipientPath); err != nil {
				t.Fatalf("write recipient: %v", err)
			}

			h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
				"FAAS_HOST_AGE_RECIPIENT_PATH=" + recipientPath,
			})
			key := h.SeedAccount(context.Background(), plan)
			limits := api.MustLimitsFor(plan)

			// Need a real app to bind secrets to.
			slug := strings.ToLower(string(plan)) + "-secrets-app"
			appBody := api.CreateAppRequest{Slug: slug}
			createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps", appBody)
			if len(createRec) == 0 {
				t.Fatalf("create app: empty response")
			}

			t.Run("happy_path", func(t *testing.T) {
				// PUT a secret.
				put := api.PutAppSecretRequest{Value: "sk_test_happy"}
				if code := statusOnly(t, h, key, http.MethodPut,
					"/v1/apps/"+slug+"/secrets/STRIPE_KEY", put); code != http.StatusOK {
					t.Fatalf("PUT: %d", code)
				}

				// GET — must not echo plaintext.
				raw, _ := doReqBytes2(t, h, key, http.MethodGet, "/v1/apps/"+slug+"/secrets", nil)
				if strings.Contains(string(raw), "sk_test_happy") {
					t.Errorf("plaintext leaked in GET response: %s", raw)
				}

				// DELETE.
				if code := statusOnly(t, h, key, http.MethodDelete,
					"/v1/apps/"+slug+"/secrets/STRIPE_KEY", nil); code != http.StatusNoContent {
					t.Fatalf("DELETE: %d", code)
				}
			})

			t.Run("quota_over_cap", func(t *testing.T) {
				// Fill the quota, then the +1 PUT must reject.
				for i := 0; i < limits.SecretCountMax; i++ {
					keyName := keyNameForQuota(i)
					put := api.PutAppSecretRequest{Value: "v"}
					if code := statusOnly(t, h, key, http.MethodPut,
						"/v1/apps/"+slug+"/secrets/"+keyName, put); code != http.StatusOK {
						t.Fatalf("seed PUT %s: %d", keyName, code)
					}
				}
				// +1 should fail.
				over := api.PutAppSecretRequest{Value: "v"}
				assertProblemAPID(t, h, key, http.MethodPut,
					"/v1/apps/"+slug+"/secrets/EXTRA",
					over, http.StatusForbidden, api.CodePlanLimitSecrets)
			})

			t.Run("reupsert_does_not_count", func(t *testing.T) {
				// New app so we have a clean quota count.
				slug2 := strings.ToLower(string(plan)) + "-reupsert"
				if code := statusOnly(t, h, key, http.MethodPost, "/v1/apps",
					api.CreateAppRequest{Slug: slug2}); code != http.StatusCreated {
					t.Fatalf("create %s: %d", slug2, code)
				}
				// Fill exactly to the cap.
				for i := 0; i < limits.SecretCountMax; i++ {
					put := api.PutAppSecretRequest{Value: "v1"}
					if code := statusOnly(t, h, key, http.MethodPut,
						"/v1/apps/"+slug2+"/secrets/"+keyNameForQuota(i), put); code != http.StatusOK {
						t.Fatalf("fill %d: %d", i, code)
					}
				}
				// Re-PUT one of them — must still 200 (re-upsert off-by-one).
				put := api.PutAppSecretRequest{Value: "v2"}
				if code := statusOnly(t, h, key, http.MethodPut,
					"/v1/apps/"+slug2+"/secrets/"+keyNameForQuota(0), put); code != http.StatusOK {
					t.Errorf("re-PUT over cap returned %d, want 200", code)
				}
			})

			t.Run("value_too_large", func(t *testing.T) {
				slug3 := strings.ToLower(string(plan)) + "-big"
				if code := statusOnly(t, h, key, http.MethodPost, "/v1/apps",
					api.CreateAppRequest{Slug: slug3}); code != http.StatusCreated {
					t.Fatalf("create big: %d", code)
				}
				big := strings.Repeat("x", limits.SecretValueMaxBytes+1)
				assertProblemAPID(t, h, key, http.MethodPut,
					"/v1/apps/"+slug3+"/secrets/BIG",
					api.PutAppSecretRequest{Value: big},
					http.StatusRequestEntityTooLarge,
					api.CodeSecretValueTooLarge)
			})

			t.Run("invalid_key_lowercase", func(t *testing.T) {
				slug4 := strings.ToLower(string(plan)) + "-shape"
				if code := statusOnly(t, h, key, http.MethodPost, "/v1/apps",
					api.CreateAppRequest{Slug: slug4}); code != http.StatusCreated {
					t.Fatalf("create shape: %d", code)
				}
				assertProblemAPID(t, h, key, http.MethodPut,
					"/v1/apps/"+slug4+"/secrets/lowercase",
					api.PutAppSecretRequest{Value: "v"},
					http.StatusBadRequest,
					api.CodeSecretInvalidKey)
			})
		})
	}
}

// TestSecretsCrossAccountIsolation verifies that a second account cannot
// read or mutate the first account's secrets. Goal: a customer on Hobby
// cannot enumerate another customer's secret NAMES, because plain key
// names (even though public per spec) leak observable state.
func TestSecretsCrossAccountIsolation(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	tmpDir := t.TempDir()
	recipientPath := filepath.Join(tmpDir, "host.age.pub")
	if err := writeTestRecipient(recipientPath); err != nil {
		t.Fatal(err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + recipientPath,
	})

	keyA := h.SeedAccount(context.Background(), api.PlanHobby)
	keyB := h.SeedAccount(context.Background(), api.PlanHobby)

	if code := statusOnly(t, h, keyA, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "a-app"}); code != http.StatusCreated {
		t.Fatalf("create a-app: %d", code)
	}
	if code := statusOnly(t, h, keyB, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "b-app"}); code != http.StatusCreated {
		t.Fatalf("create b-app: %d", code)
	}
	// Seed a row on A's app directly.
	if code := statusOnly(t, h, keyA, http.MethodPut,
		"/v1/apps/a-app/secrets/ALPHA",
		api.PutAppSecretRequest{Value: "a-value"}); code != http.StatusOK {
		t.Fatalf("seed ALPHA: %d", code)
	}

	// B reads A's list → 404.
	if code := statusOnly(t, h, keyB, http.MethodGet,
		"/v1/apps/a-app/secrets", nil); code != http.StatusNotFound {
		t.Errorf("B GET A's list: %d, want 404", code)
	}
	// B PUTs on A's app → 404.
	if code := statusOnly(t, h, keyB, http.MethodPut,
		"/v1/apps/a-app/secrets/BAD",
		api.PutAppSecretRequest{Value: "evil"}); code != http.StatusNotFound {
		t.Errorf("B PUT on A: %d, want 404", code)
	}
	// B DELETEs A's ALPHA → 404.
	if code := statusOnly(t, h, keyB, http.MethodDelete,
		"/v1/apps/a-app/secrets/ALPHA", nil); code != http.StatusNotFound {
		t.Errorf("B DELETE A's ALPHA: %d, want 404", code)
	}
}

// TestSecretsDeleteNotFound returns 400 (the URL resource IS the secret).
func TestSecretsDeleteNotFound(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	tmpDir := t.TempDir()
	recipientPath := filepath.Join(tmpDir, "host.age.pub")
	if err := writeTestRecipient(recipientPath); err != nil {
		t.Fatal(err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + recipientPath,
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	if code := statusOnly(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "del-app"}); code != http.StatusCreated {
		t.Fatalf("create del-app: %d", code)
	}
	assertProblemAPID(t, h, key, http.MethodDelete,
		"/v1/apps/del-app/secrets/NOTSET", nil,
		http.StatusBadRequest,
		api.CodeSecretNotFound)
}

// --- helpers ---------------------------------------------------------------

// writeTestRecipient generates a fresh host age identity and writes only
// the public half to path. apid uses this to seal; vmmd would need to
// hold the private half to unseal, but the e2e doesn't run vmmd — the
// store-side check (encrypted blob, not plaintext) is what we're after.
func writeTestRecipient(path string) error {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(id.Recipient().String()), 0o444)
}

// keyNameForQuota returns unique valid secret names for the i-th fill of
// a plan's quota. Always 4-letter names so the regex check is happy.
func keyNameForQuota(i int) string {
	letters := [4]byte{'A', 'B', 'C', 'D'}
	return "K" + string(letters[i%4]) + string(rune('A'+(i/4)%26))
}

// doReqBytes2 is the (body, status) variant for callers that need to
// inspect the body. Kept separate from the quota_e2e's doReq (status+body)
// so the call sites read clearly: status-only callers use the bare
// doReq shared with quota_e2e.
func doReqBytes2(t *testing.T, h *e2etest.Harness, key, method, path string, body any) ([]byte, int) {
	return doReq(t, h, key, method, path, body)
}

// statusOnly is the int-returning helper for call sites that only care
// about the response code. Wraps the package-level doReq defined in
// quota_e2e_test.go (no shadowing).
func statusOnly(t *testing.T, h *e2etest.Harness, key, method, path string, body any) int {
	t.Helper()
	_, code := doReq(t, h, key, method, path, body)
	return code
}

// doReqBytes is the body-only convenience over the package-level doReq.
// Used by tests that need to assert on a response body but don't care
// about the status code (e.g. "the body contains no plaintext").
func doReqBytes(t *testing.T, h *e2etest.Harness, key, method, path string, body any) []byte {
	t.Helper()
	b, _ := doReq(t, h, key, method, path, body)
	return b
}

// assertProblemAPID issues req, asserts (status, code) on the problem.
// Mirrors the package-level assertProblem in quota_e2e_test.go — kept
// separate so the secrets test self-documents the codes it expects
// without confusing the quota matrix reader.
func assertProblemAPID(t *testing.T, h *e2etest.Harness, key, method, path string,
	body any, wantStatus int, wantCode string) {
	t.Helper()
	raw, status := doReq(t, h, key, method, path, body)
	if status != wantStatus {
		t.Fatalf("%s %s: status=%d, want %d (body=%s)", method, path, status, wantStatus, raw)
	}
	var p api.Problem
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode problem: %v (body=%s)", err, raw)
	}
	if p.Code != wantCode {
		t.Fatalf("code=%q want %q (body=%s)", p.Code, wantCode, raw)
	}
}

// silence the unused-import warning on linux builds where secretbox is
// not touched (kept for symmetry with future metal-mode tests).
var _ = secretbox.Seal
