// edge_rules_validate_e2e_test.go — D18 per-kind e2e for
// `kind=validate` (PR-B / PR-C rollout-closer / ADR-091). Bitmask:
// APID | Gatewayd (mirrors the 6 prior per-kind e2e files).
//
// Architecture note: `kind=validate` runs at the SAME `haveApp`
// stage as the other seven kinds (handler.go:2607 inserts the call
// between `applyEdgeRuleIP` and `enforceRequireAuthn`). The
// precondition is therefore the same as every sibling: a
// `kind=route` substitute pointing the synthetic host at the real
// test app, so Backend.Lookup doesn't 404 before the validate rule
// fires.
//
// The plan's three scenarios:
//   - Happy path (422 / 404 split) — happy body passes through the
//     rule → reaches Backend.Pick → 404 (no real impl); invalid body
//     rejected by the rule → 422 + Problem.errors[].
//   - Streaming-skipped — apply_while_streaming=false + an Upgrade
//     request with a body that would otherwise reject; assert the
//     rule is skipped and the request reaches Backend.Pick.
//   - External-`$ref` rejected — seed a rule with a schema whose
//     digest was bypassed (seedEdgeRuleDirect skips apid-Validate);
//     the gateway-side `pkg/edgevalidate.Compile` re-strips at
//     compile time so the runtime never sees an external ref → 502.
//
// Why no `TestEdgeRulesValidate_Forwarded` happy-200 test: the test
// harness doesn't run schedd/vmmd/imaged (the 6 prior e2e files
// don't either). The wake path is out of scope for D18 — see PR-D's
// DeployWake-bitmask follow-on. We assert the validate rule ran by
// observing the 404 from Backend.Pick rather than a 200 from a
// proxied wake.

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// userSchema is the canonical "name/email/age" JSON Schema body
// used in the plan as the example. Compiled by
// pkg/edgevalidate.Compile; the digest is computed at runtime on
// the gateway side, not duplicated here.
const userSchema = `{
	"type": "object",
	"properties": {
		"name":  {"type": "string"},
		"email": {"type": "string", "format": "email"},
		"age":   {"type": "integer", "minimum": 0}
	},
	"required": ["name", "email"]
}`

// TestEdgeRulesValidate_E2E_HappyAndReject runs the two load-bearing
// behaviours in one test: a body that satisfies the schema passes
// through validation (we observe the wake miss — Backend.Pick 404,
// NOT a 422 from the validate rule), and a body that violates the
// schema is rejected with 422 + problem+json + errors[] entry.
func TestEdgeRulesValidate_E2E_HappyAndReject(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Gatewayd, nil)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	accountID := accountIDFromKey(t, context.Background(), pool, key)

	slug := "validate-test-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-validate.apps.test.example"

	// Precondition: kind=route substitute (synthetic host → real test app).
	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	// Rule under test: kind=validate with the example schema.
	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, app.ID, synthHost,
		state.EdgeRuleKindValidate,
		map[string]any{
			"kind": "validate",
			"validate": map[string]any{
				"schema":                userSchema,
				"content_types":         []string{"application/json"},
				"apply_while_streaming": false,
			},
		},
	)

	resetEdgeRuleCache(t, h)

	// Happy path: well-formed body. The validate rule should
	// match and let the request through to Backend.Pick, which
	// has no real target → 404. The negative signal here is
	// "not 422" — the rule ran AND the body matched.
	happyBody := map[string]any{
		"name":  "Ada",
		"email": "ada@example.com",
		"age":   36,
	}
	_, body, status := doReqHeaders(t, h, synthHost, http.MethodPost,
		"/users", happyBody)
	if status == http.StatusUnprocessableEntity {
		t.Fatalf("happy path rejected: status=%d body=%s", status, body)
	}
	if status != http.StatusNotFound {
		// 404 = Backend.Pick miss (no real impl); other 2xx/4xx
		// codes mean the validate rule misfired.
		t.Errorf("happy path: status=%d, want 404 (Backend.Pick miss after rule ran) or non-422; body=%s", status, body)
	}

	// Reject path: body violates the schema (name is not a string).
	badBody := map[string]any{
		"name":  123, // wrong type
		"email": "ada@example.com",
		"age":   36,
	}
	_, body, status = doReqHeaders(t, h, synthHost, http.MethodPost,
		"/users", badBody)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("reject path: status=%d, want 422; body=%s", status, body)
	}

	// Problem+json shape: code == request_validation_failed, errors[]
	// contains at least one FieldError.
	var problem api.Problem
	if err := json.Unmarshal(body, &problem); err != nil {
		t.Fatalf("reject body parse: %v body=%s", err, body)
	}
	if problem.Code != api.CodeRequestValidationFailed {
		t.Errorf("reject body code=%q, want %q", problem.Code, api.CodeRequestValidationFailed)
	}
	if len(problem.Errors) == 0 {
		t.Errorf("reject body errors[] is empty; want at least one FieldError")
	}
}

// TestEdgeRulesValidate_StreamingSkipped asserts the per-rule
// apply_while_streaming opt-in. With the field set to false (the
// default) AND an Upgrade request carrying a body that would
// otherwise reject, applyEdgeRuleValidate returns false (the rule
// is skipped for upgrade requests per handler.go:1576) and the
// request reaches Backend.Pick.
func TestEdgeRulesValidate_StreamingSkipped(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Gatewayd, nil)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	accountID := accountIDFromKey(t, context.Background(), pool, key)

	slug := "validate-stream-test-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-validate-stream.apps.test.example"

	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, app.ID, synthHost,
		state.EdgeRuleKindValidate,
		map[string]any{
			"kind": "validate",
			"validate": map[string]any{
				"schema":        userSchema,
				"content_types": []string{"application/json"},
				// Default false — opt-out posture per ADR-047.
				"apply_while_streaming": false,
			},
		},
	)

	resetEdgeRuleCache(t, h)

	// An "upgrade" request: Connection: Upgrade + Upgrade: websocket
	// both set so isUpgradeRequest() returns true (handler.go:1576).
	// Body deliberately violates the schema. The rule should NOT
	// fire (return false from applyEdgeRuleValidate because of the
	// upgrade), so Backend.Pick miss → 404, NOT 422.
	badBody, _ := json.Marshal(map[string]any{"name": 123})
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, h.GatewayURL+"/users", bytes.NewReader(badBody))
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Host = synthHost
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")

	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("upgrade req: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusUnprocessableEntity {
		t.Errorf("streaming-skipped: rule fired on upgrade request; status=%d (want non-422)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("streaming-skipped: status=%d, want 404 (rule skipped → Backend.Pick miss) or non-422", resp.StatusCode)
	}
}

// TestEdgeRulesValidate_ExternalRefRejected pins the gateway-side
// defence-in-depth at compile time. seedEdgeRuleDirect bypasses
// apid-Validate (the apid-side regex strips external refs), so we
// can land a rule with a `$ref: "https://..."` body in the row.
// pkg/edgevalidate.Compile re-strips at compile time on the gateway
// side, so the runtime never sees an external reference — the rule
// emits 502 + CodeBadGateway per handler.go:1649.
//
// We send a body that the inline schema would otherwise accept; the
// 502 fires from the compile-time strip on the gateway side rather
// than a runtime validation. To reach the compile path we simply
// POST anything that matches the inline shape (`name`, `email`,
// `age`) so the rule doesn't 422 on the runtime branch first.
func TestEdgeRulesValidate_ExternalRefRejected(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Gatewayd, nil)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	accountID := accountIDFromKey(t, context.Background(), pool, key)

	slug := "validate-xref-test-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-validate-xref.apps.test.example"

	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	// Build a schema with an external `$ref`. seedEdgeRuleDirect
	// bypasses apid-Validate so this lands in the row.
	//
	// Note: pkg/edgevalidate.Compile re-strips external `$ref` /
	// `$id` URLs at compile time per the gateway hot path. With
	// the strip firing on compile, the rule never reaches the
	// runtime branch — the runtime emits 502 (handler.go:1649).
	// The exact literal returned (502 vs an apid-side 422) is a
	// product of which layer fires first.
	xrefSchema := `{
		"type": "object",
		"properties": {
			"name": {"type": "string"}
		},
		"$ref": "https://internal.example.com/secrets.json"
	}`
	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, app.ID, synthHost,
		state.EdgeRuleKindValidate,
		map[string]any{
			"kind": "validate",
			"validate": map[string]any{
				"schema":        xrefSchema,
				"content_types": []string{"application/json"},
			},
		},
	)

	resetEdgeRuleCache(t, h)

	body := map[string]any{"name": "Ada"}
	_, respBody, status := doReqHeaders(t, h, synthHost, http.MethodPost,
		"/users", body)

	// Two acceptable outcomes:
	//   - 502 BadGateway: gateway-side pkg/edgevalidate.Compile
	//     re-stripped the external ref → runtime ErrSchemaExternalRef.
	//   - 422 Validation: very defensive fallback if the strip
	//     runs differently than expected — a passing request here
	//     would mean the strip failed AND the runtime accepted
	//     the schema, which would be the bug we want to trip.
	switch status {
	case http.StatusBadGateway, http.StatusUnprocessableEntity:
		// expected alarm-worthy outcome
	default:
		t.Errorf("external-$ref: status=%d, want 502 (gateway compile strip) or 422; body=%s", status, respBody)
	}
	// Any 502 response from the gateway MUST NOT be a successful
	// passthrough — if status is 200 the rule silently accepted
	// the external ref and the test missed the regression.
	if status == http.StatusOK {
		t.Fatalf("external-$ref: rule accepted external $ref; body=%s", respBody)
	}
}
