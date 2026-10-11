// Package main — handlers_rekey.go (ADR-089 PR-C).
//
// GET /v1/admin/secrets/rekey-progress returns the current snapshot
// of the background re-seal walk (pkg/rekey.Replayer wrapped by
// cmd/apid/rekey_runner.go). Operators poll this after a host
// identity rotation to monitor when the migration completes.
//
// Auth model (issue / ADR-091 §"Two-layer gate confirmed" pattern):
//   - requireScope(api.ScopesAdminOnly...) — the API key must
//     carry the admin scope. Same chain as every other /v1/admin/*
//     route (cmd/apid/server.go:672,690,…).
//   - s.adminAllows(acct) in-handler — the operator email
//     allowlist loaded from FAAS_ADMIN_EMAILS. A leaked admin
//     key from a non-operator account cannot reach the route
//     even with the right scope.
//
// 503 vs 200: when FAAS_REKEY_ENABLED is unset (the default),
// the runner is nil. We return 503 with code="rekey_disabled"
// rather than 200 + zero counters so a misconfigured dashboard
// distinguishes "feature is off" from "feature is on and idle".
// The error code is also the operator's signal that they need
// to set the env var + restart apid to start the walk.
//
// Response shape: rekey.RekeyProgress marshalled verbatim. The
// fields are:
//
//	{
//	  "total":    <rows visited across all batches so far>,
//	  "rekeyed":  <rows successfully re-sealed under current>,
//	  "skipped":  <rows whose kid already matched current>,
//	  "failed":   <rows that errored during unseal/seal/persist>,
//	  "last_id":  "<account_id>|<app_id>|<key> — last batch's cursor>"
//	}
//
// All four counters are cumulative. Operators monitor
// (rekeyed+skipped) - failed vs total to gauge completion.
// failed > 0 is the tripwire — see docs/ops/host-age-rotation.md
// "Background re-seal" for the retry procedure.
package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// getRekeyProgress serves GET /v1/admin/secrets/rekey-progress.
// Mounted via s.requireScope(api.ScopesAdminOnly...) in server.go
// — that middleware chain runs first, then this handler enforces
// the email allowlist (the same two-layer gate every other
// /v1/admin/* route uses; see handlers_admin_obs.go for the
// pattern).
//
// When s.rekeyRunner is nil, the handler distinguishes two
// misconfigurations via the rekeyRunnerOptedIn flag set at boot
// (cmd/apid/main.go, MarkRekeyRunnerOptedIn):
//
//   - optedIn=false → FAAS_REKEY_ENABLED was unset. Detail tells
//     the operator to set the flag and restart.
//   - optedIn=true → FAAS_REKEY_ENABLED=true was set, but no host
//     age identities loaded (mfaIdentities() was empty). Detail
//     tells the operator to set FAAS_HOST_AGE_IDENTITY_PATH
//     alongside the flag — the misleading "set FAAS_REKEY_ENABLED
//     and restart" hint is replaced with the actual misconfig
//     (PR #825 review fix).
//
// In both cases we return 503, not 200+zeros, so a dashboard
// distinguishes "feature is off / misconfigured" from "feature is
// on and the table is empty".
func (s *server) getRekeyProgress(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	if s.rekeyRunner == nil {
		if s.rekeyRunnerOptedIn {
			api.WriteProblem(w, api.NewProblem(
				http.StatusServiceUnavailable,
				api.CodeRekeyNoIdentities,
				"Background re-seal is opted in (FAAS_REKEY_ENABLED=true) but no host "+
					"age identities are loaded. Set FAAS_HOST_AGE_IDENTITY_PATH=<host.age> "+
					"(and FAAS_HOST_AGE_PREVIOUS_PATH=<host.age.previous> for the "+
					"overlap window) alongside the flag, then restart apid. "+
					"See docs/ops/host-age-rotation.md for the full procedure.",
				"",
			))
			return
		}
		api.WriteProblem(w, api.NewProblem(
			http.StatusServiceUnavailable,
			api.CodeRekeyDisabled,
			"Background re-seal is not enabled on this host. "+
				"Set FAAS_REKEY_ENABLED=true and restart apid to start the walk. "+
				"See docs/ops/host-age-rotation.md for the full procedure.",
			"",
		))
		return
	}
	// Progress() is backed by an atomic.Pointer — safe under
	// concurrent calls. The snapshot is the LAST BATCH's
	// cumulative counters; mid-batch reads may see a stale total
	// by cfg.BatchSize rows, which is acceptable for an operator
	// dashboard that polls every 30s.
	//
	// Convert to the wire-shape DTO (api.RekeyProgress) so the
	// JSON tags + int64 widths match the OpenAPI spec exactly.
	// pkg/rekey stays free of api imports to keep the walk
	// primitive dependency-light.
	src := s.rekeyRunner.Progress()
	writeJSON(w, http.StatusOK, api.RekeyProgress{
		Total:   int64(src.Total),
		Rekeyed: int64(src.Rekeyed),
		Skipped: int64(src.Skipped),
		Failed:  int64(src.Failed),
		LastID:  src.LastID,
	})
}
