package main

// handlers_manifest.go — apid-side server validator for the
// `gregale.yaml` declarative manifest.
//
// This file is the apid mirror of pkg/gregalemanifest.Validate
// (ADR-090 PR-C, ADR-0NN widening). The CLI uses gregalemanifest
// directly; the apid needs the same validation surface because the
// trigger routes added in commit #6 accept an inline manifest blob
// (POST /v1/triggers:batch_create). Rather than duplicating the
// per-kind validator, this file reuses pkg/gregalemanifest verbatim
// and adds the plan-tier gating the CLI doesn't need (the CLI is
// per-machine, the apid is per-account — the same plan-cap gate
// the createCron handler applies at handlers_ext.go:1683-1687
// applies here at the manifest load site).
//
// Why a thin wrapper rather than calling gregalemanifest.Validate
// directly: the apid handler signature must return *api.Problem on
// validation failure so the existing Problem-with-extraHeaders
// round-trip works; the manifest package's error is a plain
// fmt.Errorf. The wrapper maps the manifest error onto the
// CodeAppManifestInvalid RFC 7807 code so the customer sees a
// stable, machine-readable error code from the CLI or the dashboard.

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// CodeAppManifestInvalid is the 422 RFC 7807 code a customer sees
// when an inline manifest blob in a trigger batch-create request
// fails validation. Distinct from CodeValidation (the apid body
// shape guard) because the gating policy and the actor are
// different: CodeValidation rejects malformed JSON at the wire
// boundary, CodeAppManifestInvalid rejects a structurally-valid
// YAML/JSON payload that doesn't pass the per-kind validator.
const CodeAppManifestInvalid = "app_manifest_invalid"

// validateManifest loads + validates a gregale.yaml blob from disk.
// Returns *Manifest on success and *api.Problem on validation
// failure so the handler can write the response directly.
//
//nolint:unused // reserved for the manifest scan-service reconciliation PR.
func validateManifest(dir string, acctPlan api.Plan) (*gregalemanifest.Manifest, *api.Problem) {
	m, ok, err := gregalemanifest.Load(dir)
	if err != nil {
		return nil, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
			"Invalid manifest", err.Error())
	}
	if !ok {
		// No manifest present is NOT an error — a project without a
		// gregale.yaml simply has no triggers. The handler treats
		// this as "no work to do" rather than 4xx.
		return nil, nil
	}
	if prob := validateManifestAgainstPlan(m, acctPlan); prob != nil {
		return nil, prob
	}
	return m, nil
}

// validateManifestBytes is the in-memory counterpart used by the
// trigger batch-create route (POST /v1/triggers:batch_create in
// cmd/apid/handlers_triggers.go). The byte slice is parsed via
// gregalemanifest.ParseBytes (added in this commit) and fed through
// the same plan-tier gate validateManifestAgainstPlan applies.
//
// Why a thin wrapper rather than calling gregalemanifest.ParseBytes
// directly: the apid handler signature must return *api.Problem on
// validation failure so the existing Problem-with-extraHeaders
// round-trip works; the manifest package's error is a plain
// fmt.Errorf. The wrapper maps the manifest error onto the
// CodeAppManifestInvalid RFC 7807 code so the customer sees a
// stable, machine-readable error code from the CLI or the dashboard.
func validateManifestBytes(b []byte, acctPlan api.Plan) (*gregalemanifest.Manifest, *api.Problem) {
	m, err := gregalemanifest.ParseBytes(b)
	if err != nil {
		return nil, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
			"Invalid manifest", err.Error())
	}
	if m == nil {
		// Empty payload — caller is signalling "no work to do" by
		// shipping a blank blob. Treat as absent, not error.
		return nil, nil
	}
	// Structural validation (kind/slug/etc). Per-plan tier
	// gating is delegated to validateManifestAgainstPlan below —
	// the gregalemanifest package is per-machine (CLI-side) and
	// doesn't carry plan context. The earlier ValidatePlan
	// call here (//code-review PR #1202 finding #4) referred to
	// a method that doesn't exist on this struct (only
	// gregalemanifest.Manifest.Validate exists); reverted to
	// Validate + the explicit plan-tier gate already running
	// below. The plan-tier check is the one the customer's RFC
	// 7807 response carries the cap for.
	if err := m.Validate(); err != nil {
		return nil, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
			"Invalid manifest", err.Error())
	}
	if prob := validateManifestAgainstPlan(m, acctPlan); prob != nil {
		return nil, prob
	}
	return m, nil
}

// validateManifestAgainstPlan applies the per-plan tier gate the
// CLI doesn't need. Mirrors the createCron gate pattern at
// handlers_ext.go:1683-1687 — if any trigger in the manifest is of
// a kind the plan doesn't unlock, the gate fires BEFORE the store
// is touched.
//
// Today the gate is binary (triggers allowed or not, controlled by
// Plan.TriggersAllowed() — Free has it off, Hobby+ on). When the
// per-kind quotas land (PR-B in the trigger cluster), this
// function will grow per-kind counts against TriggerLimitPerApp /
// TriggerLimitPerAccount / TriggerBatchSizeMax.
func validateManifestAgainstPlan(m *gregalemanifest.Manifest, acctPlan api.Plan) *api.Problem {
	if m == nil {
		return nil
	}
	if !acctPlan.TriggersAllowed() {
		// The CLI is per-machine, so the CLI doesn't see this gate;
		// the apid is per-account, so a Free customer posting a
		// manifest with any trigger gets the upsell here rather
		// than per-trigger 402s during the deploy loop.
		return api.ErrPlanTriggersNotAllowed(acctPlan)
	}
	return nil
}
