// Command fakeapid is a hermetic stand-in for the real apid daemon,
// used as the smoke fixture for the public SDKs (Go, Node, Python).
// It mimics a small slice of the apid HTTP surface — five routes that
// the SDK quick-start exercises plus a /__healthz sentinel — and
// returns the canonical wire shapes from api/openapi.yaml.
//
// The fixture is stdlib-only on purpose (see go.mod): PR 5 (Node) and
// PR 6 (Python) spawn the same compiled binary from their own CI
// smoke tests, so it must not depend on the SDK module or any other
// non-stdlib Go package.
//
// Auth model: permissive. Any Authorization header (or none) is
// accepted; the fixture exists to validate wire shapes, not auth.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
)

// healthzBody is the liveness sentinel. The smoke tests poll this
// route in a loop after spawning the binary to detect "ready".
var healthzBody = mustJSON(map[string]bool{"ok": true})

// accountResponse mirrors api.AccountResponse (sdk/go/internal/api/dto.go:152).
// Only the canonical required fields are populated; GitHubInstall is
// omitted (omitempty) so the test sees a clean body.
var accountResponse = mustJSON(map[string]any{
	"id":             "0123456789abcdef0123456789abcdef",
	"email":          "ops@example.com",
	"email_verified": true,
	"plan":           "hobby",
	"status":         "active",
	"limits":         accountLimits(),
	"usage_gb_hours": 1.234,
	"app_count":      3,
	"developer_app_count": 1,
})

func accountLimits() map[string]any {
	return map[string]any{
		"plan":                  "hobby",
		"ram_mb":                256,
		"max_concurrency":       2,
		"deployed_apps":         3,
		"developer_apps":        2,
		"included_gb_hours":     50,
		"app_layer_max_mb":      512,
		"ephemeral_disk_max_mb": 512,
	}
}

// appResponse is the canonical AppResponse shape (sdk/go/internal/api/dto.go:76).
// Slug is mutated per-request for CreateApp / GetApp to echo the
// path or body value. MinInstances, Autoscale* are required to
// round-trip; EgressAllowlist materialises as [] (not null).
// ConcurrencyPerVMBound (issue #559) and EffectiveLimits are required
// by the OpenAPI schema — fakeapid advertises the same Hobby-plan
// values as the other endpoints. RequireAuthn (issue #560) is
// always false in the fake — fakeapid is an internal
// localhost-only stub that doesn't exercise the per-deployment
// token gate; production gated-app traffic routes through
// gatewayd-internal's authz branch (cmd/gatewayd-internal).
func appResponse(slug string) []byte {
	return mustJSON(map[string]any{
		"id":                       "app_01HXYZ",
		"slug":                     slug,
		"type":                     "app",
		"ram_mb":                   256,
		"cpu_millicores":           1000,
		"configured_resources":     appConfiguredResources(),
		"max_concurrency":          2,
		"concurrency_per_vm":       5,
		"effective_limits":         appEffectiveLimits(),
		"min_instances":            0,
		"status":                   "active",
		"url":                      "https://" + slug + ".example.com",
		"manifest":                 appManifest(),
		"egress_allowlist":         []string{},
		"autoscale_target_rps":     0,
		"autoscale_target_cpu_pct": 0,
		"require_authn":            false,
	})
}

func appConfiguredResources() map[string]any {
	return map[string]any{"memory_mb": 256, "cpu_millicores": 1000}
}

func appEffectiveLimits() map[string]any {
	return map[string]any{
		"memory_limit_mb":          256,
		"plan_memory_max_mb":       256,
		"ephemeral_disk_max_mb":    512,
		"guest_vcpus":              2,
		"cpu_limit_millicores":     1000,
		"plan_cpu_max_millicores":  1000,
		"cpu_weight":               4,
		"max_instances":            2,
		"concurrency_per_instance": 5,
		"app_request_rate_rps":     20,
		"app_request_burst":        100,
		"account_request_rate_rpm": 200,
		"request_budget_ms":        3000,
		"request_budget_max_ms":    30000,
		"response_write_timeout_s": 900,
	}
}

func appManifest() map[string]any {
	return map[string]any{
		"entrypoint": []string{"./server"},
		"port":       8080,
	}
}

// usageResponse is the canonical GetUsage wire shape: an ARRAY of
// UsageResponse objects (memory: getusage-wire-shape-mismatch.md —
// the SDK decodes []UsageResponse, never a single struct). One
// element exercises the array path; the smoke test asserts
// len(usage) == 1.
var usageResponse = mustJSON([]map[string]any{
	{
		"app_id":            "app_01HXYZ",
		"mb_seconds":        1234567,
		"requests":          42,
		"included_gb_hours": 50,
	},
})

// notFoundProblem is the canonical RFC 7807 envelope for a 404
// against /v1/apps/{slug} when slug == "missing-app-404". The SDK's
// Unwrap matches Code == "not_found" and surfaces ErrNotFound
// (sdk/go/internal/api/apierror.go). Required: title, status, code
// (api/openapi.yaml:2320). Type, detail, tx_id populated for
// realism; Limit / Observed / DocsURL / *CheckoutURL omitted.
var notFoundProblem = mustJSON(map[string]any{
	"type":   "https://docs.example.com/errors/not_found",
	"title":  "Not found",
	"status": 404,
	"code":   "not_found",
	"detail": "no app with slug 'missing-app-404'",
	"tx_id":  "fake-tx-1234",
})

// notFoundAppProblem is the same envelope but parameterised — the
// fixture returns this for any unknown GET /v1/apps/{slug} so the
// smoke test for "unknown slug" still goes through the same path.
func notFoundAppProblem(slug string) []byte {
	return mustJSON(map[string]any{
		"type":   "https://docs.example.com/errors/not_found",
		"title":  "Not found",
		"status": 404,
		"code":   "not_found",
		"detail": fmt.Sprintf("no app with slug %q", slug),
		"tx_id":  "fake-tx-1234",
	})
}

// mustJSON marshals v or panics. The fixture is built once at
// startup; marshal failure means the binary is broken before any
// request lands, so a panic is the right failure mode.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// fixture holds the runtime-mutable state. Slug creation is
// recorded in-memory so /v1/apps reflects the request body of the
// most recent POST (purely cosmetic — the smoke tests don't query
// /v1/apps after creating, but a real client might).
type fixture struct {
	mu          sync.RWMutex
	lastCreated string
}

func (f *fixture) recordCreate(slug string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastCreated = slug
}

// handler returns the http.Handler for the fixture. Extracted from
// main so main_test.go can mount the same routes against an
// httptest.Server (the SDK e2e test does this to avoid spawning a
// subprocess in unit-test runs).
func (f *fixture) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/__healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(healthzBody)
	})

	mux.HandleFunc("/v1/account", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			problemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported on /v1/account", "fixture")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(accountResponse)
	})

	// Issue #311 — programmatic auth surface. The fakeapid fixture
	// returns the canonical ProgrammaticAuthResponse so the CLI
	// exercises the full unmarshaler + saveToken() path against the
	// fixture. The plaintext is a deterministic string suffixed with
	// the request path so a test can assert which route was hit.
	programmaticAuthBody := func(route string) []byte {
		return fmt.Appendf(nil, `{"account_id":"acc_fixture_311","email":"alice@example.com","plan":"free","api_key":{"plaintext":"fp_live_fixture_%s","prefix":"fp_live_","id":"key_fixture_311"}}`, route)
	}
	mux.HandleFunc("/v1/auth/signup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			problemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported on /v1/auth/signup", "fixture")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(programmaticAuthBody("signup"))
	})
	mux.HandleFunc("/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			problemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported on /v1/auth/login", "fixture")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(programmaticAuthBody("login"))
	})
	mux.HandleFunc("/v1/auth/signup/magic-link", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			problemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported on /v1/auth/signup/magic-link", "fixture")
			return
		}
		// Anti-enumeration: the fixture always returns 200 with the
		// same body regardless of the input.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	mux.HandleFunc("/v1/apps", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// Return an array of one app so the SDK's
			// `[]AppResponse` decode exercises the slice path.
			_, _ = w.Write(mustJSON([]map[string]any{
				{
					"id":                       "app_01HXYZ",
					"slug":                     "hello-world",
					"type":                     "app",
					"ram_mb":                   256,
					"cpu_millicores":           1000,
					"configured_resources":     appConfiguredResources(),
					"max_concurrency":          2,
					"concurrency_per_vm":       5,
					"effective_limits":         appEffectiveLimits(),
					"min_instances":            0,
					"status":                   "active",
					"url":                      "https://hello-world.example.com",
					"manifest":                 appManifest(),
					"egress_allowlist":         []string{},
					"autoscale_target_rps":     0,
					"autoscale_target_cpu_pct": 0,
					"require_authn":            false,
				},
			}))
		case http.MethodPost:
			var body struct {
				Slug string `json:"slug"`
			}
			// Best-effort body decode; a malformed body still gets
			// a default "hello" slug so the smoke test can probe
			// a server-side path. (Real apid would 400; this is a
			// fixture.)
			_ = json.NewDecoder(r.Body).Decode(&body)
			slug := strings.TrimSpace(body.Slug)
			if slug == "" {
				slug = "hello"
			}
			f.recordCreate(slug)
			w.Header().Set("Content-Type", "application/json")
			// 201 Created matches the canonical OpenAPI spec
			// (api/openapi.yaml::paths./v1/apps.post.responses.201).
			// The Python generator's `_parse_response` is strict
			// (only 201 decodes to AppResponse; 200 falls through
			// to `return None` because `raise_on_unexpected_status`
			// defaults to False). The Node generator is permissive
			// (any 2xx decodes). Returning 201 here keeps all three
			// SDKs green.
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write(appResponse(slug))
		default:
			problemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET or POST is supported on /v1/apps", "fixture")
		}
	})

	mux.HandleFunc("/v1/usage", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			problemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported on /v1/usage", "fixture")
			return
		}
		// Query: month=YYYY-MM. We don't validate; the SDK sends
		// a valid month, the fixture echoes nothing back.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(usageResponse)
	})

	// /v1/apps/{slug} — net/http 1.22+ uses the trailing-slash-less
	// pattern form (Go 1.22 mux wildcard ends at "}"). For Go 1.25
	// the pattern "{slug}" matches a single segment; r.PathValue
	// extracts it. We use that to keep the handler close to the
	// real apid route shape.
	mux.HandleFunc("/v1/apps/{slug}", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			problemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is supported on /v1/apps/{slug}", "fixture")
			return
		}
		slug := r.PathValue("slug")
		if slug == "missing-app-404" {
			// Canonical sentinel: SDK expects errors.Is(err, ErrNotFound).
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(notFoundProblem)
			return
		}
		// The fixture is hermetic — it has no real persistence,
		// so any slug other than the well-known ones ("hello",
		// "hello-world") is unknown. Real apid would 200 if the
		// app exists in PG; we always 404 so the smoke tests
		// exercise the structured error path on every variant.
		if !knownSlug(slug) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(notFoundAppProblem(slug))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(appResponse(slug))
	})

	// --- ADR-124 deployment queue controls ---------------------------------
	//
	// The four routes mirror the canonical wire shapes from
	// api/openapi.yaml:4420-4535. Each route covers the happy path
	// plus a deterministic 4xx arm (404 unknown id / 409 live /
	// 409 priority out of range) so the SDK smoke tests can
	// exercise both `result.status == "..."` assertions AND the
	// `errors.Is(err, ErrNotFound)` / Problem.code branches.

	// POST /v1/apps/{slug}/deployments/{id}/cancel — CancelDeployment
	// (ADR-124 / pkg/api/client.go:3509). Body: {reason?: user|...}.
	// 200 returns the canonical CancelDeploymentResponse shape
	// (id, status:"cancelled", cancelled_at, cancel_reason,
	// cancelled_builds). 404 on unknown slug; 409 "live_forbidden"
	// when the path carries id == "live-1".
	mux.HandleFunc("/v1/apps/{slug}/deployments/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			problemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported on /v1/apps/{slug}/deployments/{id}/cancel", "fixture")
			return
		}
		slug := r.PathValue("slug")
		id := r.PathValue("id")
		if !knownSlug(slug) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(notFoundAppProblem(slug))
			return
		}
		// Mirror cmd/apid/handlers_queue_controls.go:103-105: live
		// deployments return 409 deployment_cancel_live_forbidden
		// with the canonical "use gregale deploys rollback" hint.
		// The DELETE branch below (/v1/deployments/{id}) treats
		// id == "live-1" the same way; we keep both routes in sync
		// so fixture smoke tests exercise the same code path.
		if id == "live-1" {
			problemJSON(w, http.StatusConflict, "deployment_cancel_live_forbidden", "use `gregale deploys rollback` for live deployments", "fixture")
			return
		}
		// Best-effort body decode; missing/empty body → reason ""
		// (server-side default "user" per cmd/apid/handlers_queue_controls.go:72).
		var body struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Reason == "" {
			body.Reason = "user"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(mustJSON(map[string]any{
			"id":               id,
			"status":           "cancelled",
			"cancelled_at":     "2026-08-29T00:00:00Z",
			"cancel_reason":    body.Reason,
			"cancelled_builds": []string{},
		}))
	})

	// POST /v1/deployments/{id}/reorder — ReorderDeployment
	// (ADR-124 / pkg/api/client.go:3520). Body: {priority: int}.
	// 200 returns {id, priority}. 409 with code
	// "deployment_reorder_not_pending" on id == "not-pending-1";
	// 409 with code "deployment_reorder_priority_invalid" when
	// priority < 0 or > 1000.
	mux.HandleFunc("/v1/deployments/{id}/reorder", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			problemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported on /v1/deployments/{id}/reorder", "fixture")
			return
		}
		id := r.PathValue("id")
		if id == "not-pending-1" {
			problemJSON(w, http.StatusConflict, "deployment_reorder_not_pending", "deployment is not pending; cannot reorder", "fixture")
			return
		}
		var body struct {
			Priority int `json:"priority"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Priority < 0 || body.Priority > 1000 {
			problemJSON(w, http.StatusConflict, "deployment_reorder_priority_invalid", "priority must be in [0,1000]", "fixture")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(mustJSON(map[string]any{
			"id":       id,
			"priority": body.Priority,
		}))
	})

	// DELETE /v1/deployments/{id} — ClearDeployment
	// (ADR-124 / pkg/api/client.go:3531). 200 returns
	// {id, deleted:true, deleted_at}. 409 with code
	// "deployment_cancel_live_forbidden" on id == "live-1" (mirrors
	// the apid handler at cmd/apid/handlers_queue_controls.go:213-216).
	mux.HandleFunc("/v1/deployments/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			problemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "only DELETE is supported on /v1/deployments/{id}", "fixture")
			return
		}
		id := r.PathValue("id")
		if id == "live-1" {
			problemJSON(w, http.StatusConflict, "deployment_cancel_live_forbidden", "use `gregale deploys rollback` for live deployments", "fixture")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(mustJSON(map[string]any{
			"id":         id,
			"deleted":    true,
			"deleted_at": "2026-08-29T00:00:00Z",
		}))
	})

	// POST /v1/apps/{slug}/deployments/clear-obsolete —
	// ClearObsoleteDeployments (ADR-124 / pkg/api/client.go:3539).
	// Body: {older_than?: "168h"}. 200 returns the
	// ClearObsoleteReport shape (app_slug, count, older_than).
	// 404 on unknown slug; count is the static "3" so the SDK
	// smoke test can assert the parsed-body shape.
	mux.HandleFunc("/v1/apps/{slug}/deployments/clear-obsolete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			problemJSON(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST is supported on /v1/apps/{slug}/deployments/clear-obsolete", "fixture")
			return
		}
		slug := r.PathValue("slug")
		if !knownSlug(slug) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(notFoundAppProblem(slug))
			return
		}
		var body struct {
			OlderThan string `json:"older_than"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		olderThan := body.OlderThan
		if olderThan == "" {
			olderThan = "168h"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(mustJSON(map[string]any{
			"app_slug":   slug,
			"count":      3,
			"older_than": olderThan,
		}))
	})

	return mux
}

// knownSlug is the closed set of slugs the fixture has a canned
// AppResponse for. Anything else returns 404 — the fixture is
// hermetic, no persistence layer, so the smoke tests can rely on
// "unknown slug = 404 Problem" without setup.
func knownSlug(s string) bool {
	switch s {
	case "hello", "hello-world":
		return true
	}
	return false
}

// problemJSON writes a minimal RFC 7807 envelope. Used for
// method-not-allowed cases where a full Problem is overkill but
// consistency with the canonical 404 path helps the SDK decode.
func problemJSON(w http.ResponseWriter, status int, code, detail, txID string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "https://docs.example.com/errors/" + code,
		"title":  http.StatusText(status),
		"status": status,
		"code":   code,
		"detail": detail,
		"tx_id":  txID,
	})
}

// addrFromEnv returns the listen address. Bound to 127.0.0.1 only
// (not 0.0.0.0) — the fixture is for SDK smoke tests, never
// externally reachable.
func addrFromEnv() string {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8123"
	}
	return "127.0.0.1:" + port
}

// listen acquires a free port when PORT is unset (lets the smoke
// test pick an unused port to avoid collisions in CI). When PORT
// is set explicitly, we trust the caller's value and bind it.
func listen(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

func main() {
	f := &fixture{}
	addr := addrFromEnv()
	ln, err := listen(addr)
	if err != nil {
		log.Fatalf("fakeapid: listen %s: %v", addr, err)
	}
	log.Printf("fakeapid listening on http://%s", ln.Addr().String())
	// http.Serve returns http.ErrServerClosed only when the
	// listener is closed via Shutdown/Close on a *http.Server.
	// We don't trap SIGTERM here — the smoke test sends SIGKILL
	// via the process group (Setpgid:true), so the binary
	// terminates without draining connections. That is the
	// correct contract for a hermetic fixture.
	if err := http.Serve(ln, f.handler()); err != nil {
		log.Fatalf("fakeapid: serve: %v", err)
	}
}
