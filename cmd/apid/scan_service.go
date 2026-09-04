package main

// scan_service.go — Phase 3 scan service.
//
// Inputs:  a multipart upload (source=<tar.gz>) + form fields
//          (project_slug, production_branch, install_id, only).
// Outputs: a scanPlanResponse carrying Workloads + Managed + Crons
//          + ProjectScanSource + canApply + planToken, or a
//          *api.Problem describing why the scan was rejected.
//
// The split between this file and handlers_decompose.go is
// intentional: handlers stay ≤50 lines (project guideline) and
// orchestrate the auth/middleware/notifier boundary; this file is
// pure logic and unit-testable from an httptest harness.
//
// planToken is a base64-JSON blob carrying {Hash, AccountID, TS}.
// The apply handler verifies SHA-256(uploaded_bytes) == Hash; if it
// doesn't, it re-runs the scan from scratch and re-evaluates the
// quota gate before persisting. This keeps the server authoritative
// — the plan is an optimization for the interactive flow, not a
// trust token.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// planTokenWire is the JSON shape baked into the plan token. The
// apply handler decodes this; mismatched account_id is a forgery
// signal and aborts with 403. Hash is the SHA-256 of the uploaded
// source bytes (hex). TS is informational — the hash is the load-
// bearing field.
type planTokenWire struct {
	Hash      string `json:"hash"`
	AccountID string `json:"account_id"`
	Slug      string `json:"slug"`
	TSUnix    int64  `json:"ts_unix"`
}

// scanPlanRequest is the parsed multipart body for both /scan and
// /apply. The handler builds this from the multipart stream before
// dispatching to the scan service.
type scanPlanRequest struct {
	SourcePath   string // spool path after validateAndSpool + extractTarGzToDir
	SourceSHA256 string // hex digest of the original compressed bytes
	// ScanDir is the extracted source tree. Cleaned up by scanService's
	// defer (task #19 fix; pre-fix was "cleaned up by caller" but no
	// caller ever removed it, so every successful scan leaked the dir).
	ScanDir     string
	ProjectSlug string
	ProdBranch  string
	InstallID   int64
	Only        map[string]bool // ADR-050: allowlist filter on scan
	// Exclude is the ADR-124 inverse-allowlist. Slugs in Exclude
	// are filtered out of filteredW *before* reconcile runs on the
	// apply path so the operator choice cannot be overridden by a
	// stale scan filter. Same case-insensitive name match rule as
	// Only. intersect(Only, Exclude) is rejected pre-scan with
	// code="exclude_only_overlap".
	Exclude map[string]bool
	// PersistExclude (ADR-124 follow-up #3, PR-B commit 5) is the
	// write-side complement to Exclude. When true on a successful
	// apply path, the apply handler calls
	// CreateDeploymentScopeExclusion per excluded slug, recording
	// the operator's "I excluded this for the long haul" intent.
	// Default false. The scan path accepts and ignores.
	PersistExclude bool
}

// scanPlanResponse is the body returned by the scan service and
// the apply service. Marshaled to JSON for both the /scan and
// /apply responses so the CLI can pass --json through verbatim.
//
// Workloads and Managed are the *api DTOs (not the reposcan
// types) so the wire shape is consistent with the OpenAPI spec
// and the SDK/test decoders. reposcan.Tier is a typed int; the
// DTO's Tier is a string — using the DTO here is what makes
// `plan.workloads[i].tier` serialize as "compose" instead of
// `8`. The conversion lives in toPlanWorkload below.
type scanPlanResponse struct {
	ProjectSlug  string                  `json:"project_slug"`
	RepoFullName string                  `json:"repo_full_name,omitempty"`
	ScanSource   state.ProjectScanSource `json:"scan_source"`
	Tier         string                  `json:"tier"`
	Workloads    []api.PlanWorkload      `json:"workloads"`
	Managed      []api.PlanManaged       `json:"managed"`
	Crons        []planCron              `json:"crons"`
	// CronNames parallels Crons: when /apply runs, the apply handler
	// uses CronNames[i] to look up the freshly inserted app_id from
	// insertedApps (matched by Slug == WorkloadName). Not exposed
	// on /scan responses because the scan handler doesn't need it.
	CronNames     []string `json:"-"`
	Warnings      []string `json:"warnings,omitempty"`
	ObservedApps  int      `json:"observed_apps"`
	ObservedCrons int      `json:"observed_crons"`
	LimitApps     int      `json:"limit_apps"`
	LimitCrons    int      `json:"limit_crons"`
	CanApply      bool     `json:"can_apply"`
	NotAllowed    bool     `json:"crons_not_allowed,omitempty"`
	// ADR-124 can_apply rescue signal. PreExclude is the gate
	// evaluated on the full scan (result.Workloads, pre-`--only`/
	// pre-`--exclude`); Rescued is the diff between pre and post
	// — true when --exclude flipped a blocked gate to allowed.
	// Reasons is the human-readable failure list for the post-
	// exclude state; the dashboard renders it verbatim in the
	// gate card so the operator sees why a still-blocked gate
	// is blocked.
	CanApplyPreExclude   bool     `json:"can_apply_pre_exclude,omitempty"`
	GateRescuedByExclude bool     `json:"gate_rescued_by_exclude,omitempty"`
	CanApplyReasons      []string `json:"can_apply_reasons,omitempty"`
	PlanToken            string   `json:"plan_token"`
	// PersistExclude (ADR-124 follow-up #3, PR-B commit 5) is the
	// operator's write-side intent captured from the multipart
	// persist_exclude field. The scan path accepts and ignores it;
	// the apply path uses it to decide whether to call
	// CreateDeploymentScopeExclusion per excluded slug on a
	// successful apply. Internal — not serialized to JSON
	// (consumers see the effect via PersistedExclusions on the
	// response when the persisted set carries forward, and via
	// audit log rows of kind=project.scope.excluded).
	PersistExclude bool `json:"-"`
	// ADR-124 blast-radius partition. WillDeploy + Unaffected
	// enumerate the scan-workload existence against every non-deleted
	// app in the account keyed by (RootDir, Name). Skipped is the
	// operator --exclude subset of WillDeploy (Stage 4). Removed is
	// the destructive subset reconcile will SoftDeleteAppCascade:
	// preview stamps it from partition.Removed (project-scoped),
	// apply stamps it from removedSlugs (the canonical reconcile
	// engine result). The two paths must agree on the set; see
	// computeAffectedPartition for the partition rule.
	WillDeploy []api.PlanAffectedApp `json:"will_deploy,omitempty"`
	Unaffected []api.PlanAffectedApp `json:"unaffected,omitempty"`
	Skipped    []api.PlanAffectedApp `json:"skipped,omitempty"`
	Removed    []string              `json:"removed,omitempty"`
	// PersistedExclusions (ADR-124 follow-up #3) mirrors the
	// persisted --exclude set the apply path folded in from
	// deployment_scope_exclusions. Empty on preview; non-empty on
	// apply when the operator's persisted intent took effect.
	PersistedExclusions []string `json:"persisted_exclusions,omitempty"`
	// StalePersistedExclusions (code-review fix #2) is the subset
	// of PersistedExclusions whose slug no longer exists in the
	// current scan. We do NOT add these to req.Exclude — that
	// would trip exclude_unknown_slug (400) and lock the
	// operator out of subsequent deploys until they run
	// `gregale deployments exclude clear --slug=...`. Surfacing
	// the stale set lets the dashboard render a "persisted
	// exclusion ignored (workload no longer in repo)" badge
	// instead. The janitor at PurgeOrphanedScopeExclusions
	// reaps these rows past the 90-day retention window.
	StalePersistedExclusions []string `json:"stale_persisted_exclusions,omitempty"`
}

// toPlanWorkload translates a reposcan.Workload into the wire-
// shape DTO. The only non-trivial field is Tier: reposcan.Tier is
// a typed int (1/3/5/8) and the wire shape is the .String()
// representation ("single"/"convention"/"workspace"/"compose"/
// "unknown"), matching the OpenAPI PlanWorkload.tier enum.
func toPlanWorkload(w reposcan.Workload) api.PlanWorkload {
	return api.PlanWorkload{
		Name:       w.Name,
		RootDir:    w.RootDir,
		Dockerfile: w.Dockerfile,
		Command:    w.Command,
		Class:      string(w.Class),
		Schedule:   w.Schedule,
		Ports:      w.Ports,
		EnvKeys:    w.EnvKeys,
		Source:     w.Source,
		Tier:       w.Tier.String(),
	}
}

// workloadKey is the (RootDir, Name) tuple used to match scan
// workloads against existing state.App rows. mirrors
// pkg/reposcan.Workload.Key() — server-side match is authoritative.
// Two scans producing the same workload name but different
// root_dir are NOT the same existing app (monorepo service-api vs
// apps/api).
type workloadKey struct {
	RootDir string
	Name    string
}

// affectedPartition is the ADR-124 blast-radius projection over the
// scan workload set vs the account's existing app rows. PlanWorkload
// already carries (RootDir, Name) in the carrier, but the partition
// surface lives in api.PlanAffectedApp so the wire DTO can carry
// Action + ID + ExistingRootDir.
//
// Removed is the destructive subset reconcile will
// SoftDeleteAppCascade on apply. Project-scoped (projectID parameter)
// so a multi-project account doesn't see its other projects' apps
// in the destructive preview. The preview and apply paths produce
// the same Removed set because both honor (RootDir, Name) +
// --exclude + project scope — pkg/reconcile.diff.workloadDiff is
// the canonical implementation; computeAffectedPartition mirrors
// it for the wire projection so the operator sees what will
// actually be deleted before clicking Apply.
type affectedPartition struct {
	WillDeploy []api.PlanAffectedApp
	Unaffected []api.PlanAffectedApp
	// Skipped is the operator --exclude subset of scan workloads.
	// Visible on the dashboard as "excluded by operator". Action
	// is always "noop" — the apply path skips these entirely.
	Skipped []api.PlanAffectedApp
	// Removed is the destructive subset (project-scoped). Action
	// vocabulary "remove" is not stamped here — the wire shape is
	// []string per ADR-124 §1; the dashboard renders the same
	// shape. See TestPlanResponse_RemovedShape for the pin.
	Removed []string
}

// computeAffectedPartition partitions the post-`--only` filtered scan
// workloads vs the account's existing app rows. Existing apps are
// loaded via s.store.ListApps(ctx, acct.ID); the projection is
// O(len(filteredW) + len(existing)) and the result is keyed
// consistently with pkg/reconcile.diff.workloadDiff so the wire and
// the apply engine do not diverge.
//
// allScanWl is the unfiltered result.Workloads (post-`--only`, but
// pre-`--exclude`) — Skipped needs every excluded workload even
// when it would have been dropped by filteredW. exclude is the
// case-insensitive set from scanPlanRequest.Exclude; an empty map
// produces no Skipped rows.
//
// Action vocabulary (ADR-124):
//
//	"create" — scan workload, no existing app.
//	"update" — scan workload, existing app matches (RootDir, Name).
//	"noop"   — either (a) existing app, no scan workload, OR (b)
//	           operator --exclude hit (Skipped, surfaced separately).
//	"remove" — surfaced on the Removed []string slice (preview and
//	           apply paths agree on the set; see type comment above).
//
// projectID is the unique ID of the project being previewed. Empty
// (brand-new project, not yet inserted) makes the Removed loop a
// no-op because no existing app can have a matching ProjectID.
func computeAffectedPartition(
	filteredW []reposcan.Workload,
	allScanWl []reposcan.Workload,
	existingApps []state.App,
	exclude map[string]bool,
	projectID string,
) affectedPartition {
	idx := make(map[workloadKey]state.App, len(existingApps))
	for _, a := range existingApps {
		idx[workloadKey{RootDir: a.RootDir, Name: a.WorkloadName}] = a
	}
	// scanKeys is keyed on (RootDir, Name) of every scan workload.
	// Built early (before Skipped + Unaffected + Removed loops) so
	// the dual-view Skipped branch below can skip apps that ARE in
	// the scan set; the Skipped row from the workload branch already
	// covers those (avoids a duplicate Skipped entry for the same
	// slug).
	scanKeys := make(map[workloadKey]struct{}, len(allScanWl))
	for _, w := range allScanWl {
		scanKeys[workloadKey{RootDir: w.RootDir, Name: w.Name}] = struct{}{}
	}
	// WillDeploy: filteredW (no excluded) → create or update. Order
	// preserved so the i-alignment with respWorkloads stays intact.
	will := make([]api.PlanAffectedApp, 0, len(filteredW))
	for _, w := range filteredW {
		k := workloadKey{RootDir: w.RootDir, Name: w.Name}
		row := api.PlanAffectedApp{Slug: w.Name, Action: "create"}
		if a, ok := idx[k]; ok {
			row.Action = "update"
			row.ID = a.ID
			if a.RootDir != w.RootDir {
				row.ExistingRootDir = a.RootDir
			}
		}
		will = append(will, row)
	}
	// Skipped: every scan workload (filteredW or dropped by --only)
	// whose lowercased name is in exclude, plus every existing app
	// in the account whose slug is in exclude but who is NOT in the
	// scan set (dual-view: the operator's --exclude intent applies
	// to BOTH a scan workload and a stale app row — see
	// TestScanPartition_ExcludedExistingAppDualView for the pin).
	// Order preserved: scan workloads first, then existing-app
	// rows, then sorted by Slug for determinism.
	skip := make([]api.PlanAffectedApp, 0)
	if len(exclude) > 0 {
		for _, w := range allScanWl {
			if !exclude[strings.ToLower(w.Name)] {
				continue
			}
			k := workloadKey{RootDir: w.RootDir, Name: w.Name}
			row := api.PlanAffectedApp{Slug: w.Name, Action: "noop"}
			if a, ok := idx[k]; ok {
				row.ID = a.ID
				if a.RootDir != w.RootDir {
					row.ExistingRootDir = a.RootDir
				}
			}
			skip = append(skip, row)
		}
		// Add excluded existing apps not in the scan set — these
		// are apps whose workload was removed from the repo (or
		// renamed) but the operator still wants the --exclude
		// contract honored (no soft-delete on apply). Without
		// this branch the dashboard's Skipped column would be
		// empty for the dual-view case.
		seen := make(map[string]bool, len(skip))
		for _, s := range skip {
			seen[s.Slug] = true
		}
		for _, a := range existingApps {
			if !exclude[strings.ToLower(a.Slug)] {
				continue
			}
			if seen[a.Slug] {
				continue
			}
			k := workloadKey{RootDir: a.RootDir, Name: a.WorkloadName}
			if _, hit := scanKeys[k]; hit {
				continue
			}
			skip = append(skip, api.PlanAffectedApp{
				Slug:            a.Slug,
				ID:              a.ID,
				Action:          "noop",
				ExistingRootDir: a.RootDir,
			})
			seen[a.Slug] = true
		}
	}
	// Unaffected: existing apps whose (RootDir, Name) is not in any
	// scan workload. The "no scan workload" check is across allScanWl
	// (post-`--only`) so an excluded update doesn't shift an app from
	// Unaffected to Skipped — it stays in Skipped only. scanKeys was
	// built above the Skipped loop (shared with the dual-view branch).
	unaff := make([]api.PlanAffectedApp, 0, len(existingApps))
	for _, a := range existingApps {
		k := workloadKey{RootDir: a.RootDir, Name: a.WorkloadName}
		if _, hit := scanKeys[k]; hit {
			continue
		}
		unaff = append(unaff, api.PlanAffectedApp{
			Slug:            a.Slug,
			ID:              a.ID,
			Action:          "noop",
			ExistingRootDir: a.RootDir,
		})
	}
	// Removed (ADR-124 PR-followup gap #1): the destructive subset
	// reconcile will SoftDeleteAppCascade on apply. Three filters:
	//
	//  1. ProjectID == projectID — multi-project accounts must
	//     not see their other projects' apps in the destructive
	//     preview. Unaffected stays account-scoped (blast-radius
	//     view); Removed is project-scoped (the destructive view).
	//     Empty projectID (no project_slug on the request, brand-
	//     new project not yet inserted) makes the loop a no-op
	//     because no existing app can have a matching ProjectID.
	//  2. (RootDir, WorkloadName) NOT in scanKeys — same-key apps
	//     are matched (WillDeploy), not removed. Mirrors
	//     pkg/reconcile.diff.workloadDiff:106-115 (the `removes`
	//     loop).
	//  3. WorkloadName NOT in exclude — operator wants this app
	//     left alone, not soft-deleted. Without this filter, an
	//     excluded existing app would silently appear in Removed
	//     and be soft-deleted on apply despite the operator's
	//     --exclude intent.
	//
	// Stable order (alphabetical) matches the CLI's
	// printAffectedText sort and the dashboard's ApplyResult
	// banner order, so all three surfaces render the same set
	// the same way.
	removed := make([]string, 0)
	if projectID != "" {
		for _, a := range existingApps {
			if a.ProjectID != projectID {
				continue
			}
			// Exclude by either slug OR workload name — the
			// operator's --exclude maps to the app slug (the
			// operator-visible wire name), while the
			// reconcile-engine diff keys off workload_name.
			// Apps where Slug != WorkloadName (rare, but
			// legitimate — see the dual-view test
			// TestScanPartition_ExcludedExistingAppDualView)
			// must honour the exclude contract on BOTH fields.
			// Mirrors the Skipped dual-view filter above.
			if exclude[strings.ToLower(a.Slug)] ||
				exclude[strings.ToLower(a.WorkloadName)] {
				continue
			}
			k := workloadKey{RootDir: a.RootDir, Name: a.WorkloadName}
			if _, hit := scanKeys[k]; hit {
				continue
			}
			removed = append(removed, a.Slug)
		}
		sort.Strings(removed)
	}
	return affectedPartition{
		WillDeploy: will,
		Unaffected: unaff,
		Skipped:    skip,
		Removed:    removed,
	}
}

// auditEmitterAs is the minimal *auditor surface needed to emit
// per-row audit events at preview time. Defined as an interface so
// the helper is unit-testable without standing up a real pgxpool
// (the audit_async_test pattern). *auditor (cmd/apid/audit.go:315)
// already satisfies this signature; the production wiring is
// unchanged.
type auditEmitterAs interface {
	EmitAs(ctx context.Context, actor, kind string, accountID *string, data map[string]any)
}

// emitSkippedAuditRows is the loop body extracted from
// scanService so a regression test can drive the call site
// directly (without standing up a full scanService server). It
// emits one project.workload.skipped row per partition.Skipped
// entry. `row.Slug` IS the scan workload's `Name` — for a
// brand-new excluded workload (row.ID == "", the common case
// when the scan emits a workload that hasn't been deployed
// yet), `row.Slug` carries the name we need to stamp the audit
// row. Earlier versions looked `slugToApp[row.Slug]` in the
// app table and silently skipped them, leaving the SOC 2 trail
// incomplete.
func emitSkippedAuditRows(
	ctx context.Context,
	auditor auditEmitterAs,
	actor string,
	accountID string,
	projectID string,
	sourceSHA string,
	rows []api.PlanAffectedApp,
) {
	for _, row := range rows {
		emitWorkloadSkippedRow(ctx, auditor, actor, accountID, projectID, row.ID, row.Slug, sourceSHA)
	}
}

// emitWorkloadSkippedRow fires one project.workload.skipped row
// per operator --exclude entry. Called from scanService right after
// computeAffectedPartition so each partition.Skipped row gets a
// durable audit row before the response is marshalled.
//
// The apply path runs the same scan partition so re-emitting on
// apply would double-count; preview-time emission is the source of
// truth (per pkg/reconcile.KindWorkloadSkipped doc).
//
// sourceSHA is the SHA-256 of the uploaded source tarball (req.
// SourceSHA256) — a stable identifier the audit-events table can
// group on with the deploy/apply rows.
//
// Tests pass a stub auditEmitterAs; production wiring uses *auditor.
// nil-receiver safe (mirrors cmd/apid/audit.go:316-317).
func emitWorkloadSkippedRow(
	ctx context.Context,
	auditor auditEmitterAs,
	actor string,
	accountID string,
	projectID string,
	appID string,
	workloadName string,
	sourceSHA string,
) {
	if auditor == nil {
		return
	}
	acctID := accountID
	data := map[string]any{
		"project_id":    projectID,
		"app_id":        appID,
		"workload_name": workloadName,
		"reason":        "unchanged via exclude",
		"commit_sha":    sourceSHA,
	}
	auditor.EmitAs(ctx, actor, reconcile.KindWorkloadSkipped, &acctID, data)
}

// emitPersistedExcludedAuditRows fires one project.scope.excluded
// audit row per slug carried forward from the persisted
// deployment_scope_exclusions table (code-review fix #5). Tagged
// with reason="persisted" so SOC 2 reviewers can distinguish a
// persisted fold-in from a per-deploy --exclude (which is
// emitted as reason="unchanged via exclude" via
// emitWorkloadSkippedRow above). appID is left empty here because
// the persisted set's app rows are not yet resolved at scan
// time — the apply-side handler stamps a synthetic UUID on the
// brand-new path (see handlers_decompose.go::syntheticExclusionAppID).
func emitPersistedExcludedAuditRows(
	ctx context.Context,
	auditor auditEmitterAs,
	actor string,
	accountID string,
	projectID string,
	sourceSHA string,
	slugs []string,
) {
	if auditor == nil || len(slugs) == 0 {
		return
	}
	for _, slug := range slugs {
		acctID := accountID
		auditor.EmitAs(ctx, actor, reconcile.KindProjectScopeExcluded, &acctID, map[string]any{
			"project_id":    projectID,
			"workload_name": slug,
			"reason":        "persisted",
			"commit_sha":    sourceSHA,
		})
	}
}

// toPlanManaged translates a reposcan.Managed into the wire-shape
// DTO. Pure field copy — the reposcan and DTO fields align
// one-to-one; this helper exists so the carrier conversion is
// symmetrical with toPlanWorkload and stays at one call site if
// either side grows new fields.
func toPlanManaged(m reposcan.Managed) api.PlanManaged {
	return api.PlanManaged{
		Name:    m.Name,
		Kind:    m.Kind,
		EnvHint: m.EnvHint,
		Source:  m.Source,
		Image:   m.Image,
	}
}

// evaluateQuotaGate computes the can_apply triple for the given
// (workloads, limits, observedApps, observedCrons). The function
// derives the cron count from workloads (any workload with a
// non-empty Schedule is also a cron — see scan_service.go:714-727
// for the equivalent inline loop) so callers can evaluate the gate
// on both pre-filter and post-filter workloads without sharing
// scan-internal state.
//
// Returns:
//
//	canApply    — true when the plan fits under the plan caps AND
//	               crons are allowed by the plan.
//	notAllowed  — true when crons are forbidden by the plan (the
//	               special hard-block Free plan sees on Hobby/Pro/
//	               Scale cron workloads). The flag is independent
//	               of canApply: a Free-plan operator who uploaded
//	               a cron sees both canApply=false AND
//	               notAllowed=true.
//	reasons     — human-readable list of why can_apply is false.
//	               Empty when can_apply is true. The dashboard
//	               renders these verbatim in the gate card;
//	               CanApplyReasons on PlanResponse carries this
//	               same slice.
//	cronCount   — the count derived from workloads (returned so
//	               the caller can size the partition's
//	               observed_crons wire field without re-walking
//	               workloads).
//
// Pure function; no I/O. Tests pin the three failure-mode shapes
// (apps-over, crons-not-allowed, crons-over) plus the rescue
// invariant (preCanApply=false → postCanApply=true after exclude).
func evaluateQuotaGate(
	workloads []reposcan.Workload,
	limits api.Limits,
	observedApps int,
	observedCrons int,
) (canApply bool, notAllowed bool, reasons []string, cronCount int) {
	for _, wl := range workloads {
		if wl.Schedule != "" {
			cronCount++
		}
	}

	canApply = true
	if observedApps+len(workloads) > limits.DeployedApps {
		canApply = false
		reasons = append(reasons, fmt.Sprintf(
			"apps over plan limit: %d + %d > %d",
			observedApps, len(workloads), limits.DeployedApps,
		))
	}
	if cronCount > 0 && limits.CronLimitPerAccount == 0 {
		canApply = false
		notAllowed = true
		reasons = append(reasons, "crons not allowed on this plan")
	}
	if observedCrons+cronCount > limits.CronLimitPerAccount {
		canApply = false
		reasons = append(reasons, fmt.Sprintf(
			"crons over plan limit: %d + %d > %d",
			observedCrons, cronCount, limits.CronLimitPerAccount,
		))
	}
	return
}

// gateRescueReason maps the templated reason strings
// evaluateQuotaGate emits to a closed-set vocabulary the
// apid_plan_gate_rescued_by_exclude_total counter
// (pkg/wire/metrics.go::PlanGateRescuedByExclude) labels
// with. Cardinality is bounded: 4 plans × 3 reasons = 12
// series (pre-instantiated). The "unknown" bucket catches any
// future reason string so the metric doesn't drift; today
// evaluateQuotaGate emits exactly the three prefixes handled
// here. Multi-reason rescue plans are bucketed by their
// FIRST reason — a future enhancement could surface
// one-bucket-per-reason via a histogram, but the per-bucket
// counts today are enough to triage which gate type the
// rescue pattern is most often saving operators from.
func gateRescueReason(reasons []string) string {
	if len(reasons) == 0 {
		return "unknown"
	}
	r := reasons[0]
	switch {
	case strings.HasPrefix(r, "apps over plan limit:"):
		return "apps_over_limit"
	case strings.HasPrefix(r, "crons over plan limit:"):
		return "crons_over_limit"
	case r == "crons not allowed on this plan":
		return "crons_not_allowed"
	default:
		return "unknown"
	}
}

// emitGateRescueMetric increments the apid_plan_gate_rescued_by_exclude_total
// counter for the (plan, reason) bucket the gateRescueReason classifier
// produced. ops may be nil (unit tests that don't wire metrics); the
// nil-safe accessor inside pkg/wire returns nil and the Inc() is
// skipped. Extracted into a helper so the helper imports the wire
// type — the call site at the gateRescuedByExclude block does not
// need to reference pkg/wire directly (the field s.ops already
// carries a *wire.OpsMetrics that we never see in scan_service's
// local lexical scope).
func emitGateRescueMetric(ops *wire.OpsMetrics, plan string, reasons []string) {
	if ops == nil {
		return
	}
	if c := ops.PlanGateRescuedByExclude(plan, gateRescueReason(reasons)); c != nil {
		c.Inc()
	}
}

// planCron is the cron shape returned by the scan service. We keep
// it distinct from state.Cron (which carries AppID + CreatedAt)
// because at scan time there's no AppID yet — apply resolves the
// name→ID map by inserting apps first.
type planCron struct {
	WorkloadName string `json:"workload_name"`
	Schedule     string `json:"schedule"`
	Path         string `json:"path"`
	Enabled      bool   `json:"enabled"`
}

// appliedBuild is an alias for api.AppliedBuild so the local code
// can name the wire type without importing pkg/api in every helper
// signature. Both the response field type and this alias are
// identical; the local type is a transitional convenience.
type appliedBuild = api.AppliedBuild

// applyBuildsForAddedChanged stages a per-workload tarball rooted
// at app.RootDir under FAAS_SPOOL_ROOT/projects/<account>/<project>/
// <appID>.tar.gz and enqueues one (deployment, build) per workload
// via apidsource.Enqueue. Returns one appliedBuild per input app in
// the same order. On staging or enqueue failure the per-app Error
// field is populated and the loop continues — partial success is the
// design (mirrors pkg/githubd/service.go:361-367).
//
// Lifetime: the staged tarball persists on disk after the function
// returns — builderd reads it as a local file (pkg/builderd/
// builderd.go:321). A spool GC is a follow-up issue (the plan calls
// it out explicitly); this PR does not silently leave it undocumented
// but also does not implement the GC.
//
// The SourceURL + CommitSHA fields are empty (the apply path is
// upload-from-customer-tarball, not pull-from-codeload); the helper
// handles empty values cleanly.
//
// scanDir is the path to the extracted source tree (req.ScanDir from
// the multipart parse). It must outlive the staging call but is
// removed by the handler's defer after scanService returns.
//
// r is the inbound HTTP request. MEDIUM review #2 (PR #992): the
// scan-and-apply path was the only HTTP-routed deploy surface
// that didn't stamp the four actor columns — every
// customer-triggered project apply landed in deployments with
// deployed_by_user_id=NULL and deployed_from_ip=NULL. Threading
// r through keeps cmd/apid/deploy_actor.go as the single source
// of truth (routeKindForRequest + middleware.ClientIP) rather
// than forking the actor surface across packages.
func (s *server) applyBuildsForAddedChanged(
	ctx context.Context, r *http.Request, acct state.Account, project state.Project,
	scanDir string, added, changed []state.App,
) []appliedBuild {
	touched := make([]state.App, 0, len(added)+len(changed))
	touched = append(touched, added...)
	touched = append(touched, changed...)
	out := make([]appliedBuild, 0, len(touched))
	for _, app := range touched {
		res := appliedBuild{Slug: app.Slug, AppID: app.ID}
		// Stage the per-workload tarball. Failure here (e.g. the
		// RootDir doesn't exist in the extracted tree — a reposcan
		// bug) is logged and recorded; the apply continues for the
		// other apps.
		staged, bytes, stageErr := s.stageApplyTarball(ctx, scanDir, acct.ID, project.ID, app)
		if stageErr != nil {
			// Surface a generic message on the wire — the
			// full error (which includes operator paths
			// like the spool root) goes to slog only. The
			// dashboard renders this string in the apply
			// response, and we don't want to leak server
			// layout to the customer.
			s.log.Warn("apid: apply stage tarball failed", "app_id", app.ID, "slug", app.Slug, "project_id", project.ID, "err", stageErr)
			res.Error = "stage failed (server logs carry the detail)"
			out = append(out, res)
			continue
		}
		// Enqueue via the shared helper. The helper does CreateDeployment
		// + build.log spool + UpdateDeploymentStatus(building) + CreateBuild
		// + NotifyBuildQueued + (optional) NotifyDeploymentChanged for
		// the prior row. Source="tarball" keeps the build_queued
		// payload's kind field aligned with the deployment's kind.
		enqRes, enqErr := apidsource.Enqueue(ctx, s.store, s.notif, apidsource.EnqueueParams{
			AppID:       app.ID,
			Kind:        state.DeploymentKindTarball,
			SourcePath:  staged,
			SourceBytes: bytes,
			LogSpool:    spoolRoot(),
			Log:         s.log,
			// MEDIUM review #2 (PR #992): stamp the four
			// actor columns on every scan-and-apply
			// deployment. Without these, every
			// customer-triggered project apply lands in
			// deployments with deployed_by_user_id=NULL
			// and deployed_from_ip=NULL — the SOC 2
			// CC7.2 audit question "who deployed v3 at
			// 14:32?" has no answer for this entire
			// surface. routeKindForRequest + ClientIP
			// are the same single source of truth used by
			// every other HTTP-routed deploy path.
			ActorUserID: acct.ID,
			ActorVia:    routeKindForRequest(r),
			ActorFromIP: middleware.ClientIP(r),
		})
		if enqErr != nil {
			// Same wire/server split as the stage branch
			// above: surface a generic message to the
			// customer, log the full error (which can carry
			// pgx field names, row IDs, spool paths) to
			// slog only.
			s.log.Warn("apid: apply enqueue build failed", "app_id", app.ID, "slug", app.Slug, "project_id", project.ID, "err", enqErr)
			res.Error = "enqueue failed (server logs carry the detail)"
			out = append(out, res)
			continue
		}
		res.DeploymentID = enqRes.DeploymentID
		res.BuildID = enqRes.BuildID
		out = append(out, res)
	}
	return out
}

// stageApplyTarball writes a per-workload tarball rooted at app.RootDir
// under <FAAS_SPOOL_ROOT>/projects/<accountID>/<projectID>/<appID>.tar.gz
// and returns (path, bytes, error). The dir layout keys on
// (account, project, app) so a re-apply of the same project overwrites
// the per-workload tarballs in place.
//
// The walk is delegated to githubd.RepackageRootTree — the same
// gzip-tar encoder githubd uses for push-triggered builds. Empty
// RootDir walks the whole extracted tree (single-app project);
// RootDir "/worker" walks everything under that prefix (multi-app
// project where each app is a subdir).
func (s *server) stageApplyTarball(
	ctx context.Context, scanDir, accountID, projectID string, app state.App,
) (string, int64, error) {
	dir := filepath.Join(spoolRoot(), "projects", accountID, projectID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", 0, fmt.Errorf("create spool dir: %w", err)
	}
	dst := filepath.Join(dir, app.ID+".tar.gz")
	if err := githubd.RepackageRootTree(ctx, os.DirFS(scanDir), app.RootDir, dst); err != nil {
		return "", 0, fmt.Errorf("repackage: %w", err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		return "", 0, fmt.Errorf("stat staged: %w", err)
	}
	return dst, fi.Size(), nil
}

// scanService runs the pipeline end-to-end:
//
//	multipart parse → spool+validate → extract → scan
//	  → filter --only → derive scan_source → compute can_apply
//	  → mint plan_token
//
// `apply` is true for POST /v1/projects, false for POST /v1/projects/scan.
// When apply=true, the function ALSO creates the projects row + runs
// pkg/reconcile.Service, returning the reconcile Result via the
// third/fourth slots. When apply=false, the caller renders the
// response as JSON.
//
// Returns (response, project, added, changed, removedSlugs, builds, problem).
// project/added/changed/removedSlugs/builds are valid only when
// problem == nil and apply==true.
//
// added is the post-insert state.App slice for newly-created apps;
// changed is the post-update slice for workloads whose RootDir /
// WorkloadName / StartCommand drifted. removedSlugs carries the slugs
// of workloads that the scan dropped from the project — the handler
// uses these to soft-delete the corresponding crons (issue: a cron
// for a removed workload previously 500'd because the slug→ID map
// had no entry; PR-GH.6 fixes that by walking removedSlugs).
// builds is the per-workload (deployment_id, build_id) results from
// the apply-time build enqueue loop; the handler renders them in the
// apply response.
func (s *server) scanService(
	w http.ResponseWriter, r *http.Request, acct state.Account,
	planToken string, apply bool,
) (*scanPlanResponse, state.Project, []state.App, []state.App, []string, []appliedBuild, *api.Problem) {
	limits := api.MustLimitsFor(acct.Plan)
	req, prob := parseScanMultipart(r, acct, limits)
	if prob != nil {
		return nil, state.Project{}, nil, nil, nil, nil, prob
	}
	// ADR-124 follow-up #3 — apply-time persisted-exclude fallback.
	// When the apply path runs without an explicit --exclude AND a
	// project row already exists (i.e. this is a re-deploy, not a
	// brand-new project), fold the persisted set into req.Exclude
	// so the operator's "I excluded this for the long haul" intent
	// carries forward without re-typing. The response surfaces the
	// merged set via resp.PersistedExclusions; the handler emits
	// one KindProjectScopeExcluded audit row per slug below.
	//
	// Brand-new projects (no project row yet) skip the fallback —
	// there is nothing persisted to fold in. The check is cheap
	// (single ProjectBySlug; ErrNotFound is not an error here).
	// persistedSlugs captures every slug folded in from the persisted
	// deployment_scope_exclusions table on the apply path. When the
	// operator did NOT pass --exclude on this deploy but a previous
	// deploy ran with --persist-exclude, those slugs carry forward
	// into this apply. Surfaced via resp.PersistedExclusions so the
	// operator + dashboard can see exactly what carried forward; the
	// apply handler emits one KindProjectScopeExcluded audit row per
	// slug for SOC 2 CC7.2 paper trail.
	var persistedSlugs []string
	// Code-review fix #3: drop the `len(req.Exclude) == 0` guard.
	// The original guard meant typing `--exclude=foo` while a
	// persisted set existed silently dropped the persisted
	// carry-forward — the operator's persisted intent was
	// shadowed by any per-deploy --exclude, even though the
	// intent of `--exclude=foo,bar` + persisted `baz,quux` is
	// "exclude foo + bar + baz + quux" (union semantics). Fold
	// in regardless; the post-scan check below filters slugs no
	// longer in the repo into stale_persisted_exclusions instead
	// of tripping exclude_unknown_slug (fix #2).
	if apply {
		if proj, projErr := s.store.ProjectBySlug(r.Context(), acct.ID, req.ProjectSlug); projErr == nil {
			if persisted, lookupErr := s.store.LookupDeploymentScopeExclusions(r.Context(), acct.ID, proj.ID); lookupErr == nil && len(persisted) > 0 {
				if req.Exclude == nil {
					req.Exclude = make(map[string]bool, len(persisted))
				}
				for _, e := range persisted {
					lname := strings.ToLower(e.Slug)
					req.Exclude[lname] = true
					persistedSlugs = append(persistedSlugs, lname)
				}
			}
		}
	}
	// Code-review fix #6: a persisted slug that also appears in
	// req.Only would trip the --only/--exclude mutex below with
	// 409 exclude_only_overlap. The persisted set is the
	// "long-haul" intent (per ADR-127) and the operator's
	// ephemeral --only contradicts it; honor persisted by
	// stripping the slug from req.Only before the mutex check.
	// Audit emission downstream tags the slug as reason="persisted"
	// so operators can trace what happened.
	if len(persistedSlugs) > 0 && len(req.Only) > 0 {
		persistedSet := make(map[string]bool, len(persistedSlugs))
		for _, slug := range persistedSlugs {
			persistedSet[slug] = true
		}
		for slug := range req.Only {
			if persistedSet[slug] {
				delete(req.Only, slug)
			}
		}
	}
	// ADR-124: --only and --exclude are inverse filters and cannot
	// share a slug. Reject the request pre-scan with a 409 and a
	// stable code so the CLI / dashboard can branch on it without
	// guessing. Sorted output keeps the message deterministic.
	if len(req.Exclude) > 0 && len(req.Only) > 0 {
		var clash []string
		for slug := range req.Only {
			if req.Exclude[slug] {
				clash = append(clash, slug)
			}
		}
		if len(clash) > 0 {
			sort.Strings(clash)
			return nil, state.Project{}, nil, nil, nil, nil, api.NewProblem(
				http.StatusConflict, "exclude_only_overlap",
				"workload is in both --only and --exclude",
				fmt.Sprintf("duplicate: %s", strings.Join(clash, ", ")))
		}
	}
	// Cleanup the spooled upload. Best-effort: a failure here just
	// means the original tarball lingers under FAAS_SCAN_SPOOL_ROOT
	// until the next sweep.
	defer func() { _ = os.Remove(req.SourcePath) }() //nolint:errcheck // best-effort
	// Cleanup the extracted dir on every return path. Pre-task #19
	// this was documented "cleaned up by caller" but no caller ever
	// removed it, so every successful scan leaked the extracted dir
	// under FAAS_SCAN_SPOOL_ROOT. The defer fires AFTER all the
	// in-function staging reads (applyBuildsForAddedChanged runs
	// synchronously and walks req.ScanDir before the return at the
	// bottom), so the staging sees a still-live tree.
	defer func() { _ = os.RemoveAll(req.ScanDir) }() //nolint:errcheck // best-effort

	// If a plan_token was passed (apply path), validate it BEFORE
	// running the scan. Mismatch -> 409 plan_token_stale. This
	// short-circuits the scan only when the hash matches; on a
	// miss we re-scan (the caller asked for this bytes-blob, not
	// the cached plan) and continue.
	if planToken != "" {
		var pt planTokenWire
		b, decErr := base64.StdEncoding.DecodeString(planToken)
		if decErr == nil {
			_ = json.Unmarshal(b, &pt)
		}
		if pt.AccountID != acct.ID || pt.Hash != req.SourceSHA256 {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict,
				"plan_token_stale",
				"plan_token does not match uploaded source",
				"re-run scan and apply in one flow"))
			return nil, state.Project{}, nil, nil, nil, nil, api.ErrInternal("plan_token stale")
		}
	}

	result, scanErr := reposcan.Scan(os.DirFS(req.ScanDir))
	if scanErr != nil {
		return nil, state.Project{}, nil, nil, nil, nil, api.NewProblem(
			http.StatusBadRequest, api.CodeSourceInvalid,
			"Scan failed", scanErr.Error())
	}

	// Server-side secret-scan (closes v1 gap A). Runs on BOTH
	// apply=true and apply=false — the preview contract is "we ran
	// the scan and found redactions needed", so a 422 is honest even
	// when the customer wasn't going to apply. A walk error with no
	// findings fails closed with a 500; findings drive the 422. The
	// walk is bounded by
	// serverSecretScanMaxBytes (1 MiB per file); it intentionally walks
	// excluded/build/VCS directories too because a direct tarball upload
	// must not be able to hide a secret by choosing a directory name.
	secretFindings, ssErr := scanExtractedTreeSecrets(req.ScanDir)
	if ssErr != nil && len(secretFindings) == 0 {
		// Walk-level failure with no partial findings: surface as a
		// 500 so the customer can retry. If we DO have findings, the
		// 422 takes precedence — refusing the upload is more useful
		// than a generic "scan failed" 500.
		return nil, state.Project{}, nil, nil, nil, nil, api.NewProblem(
			http.StatusInternalServerError, api.CodeInternal,
			"Secret scan failed", ssErr.Error())
	}
	if len(secretFindings) > 0 {
		return nil, state.Project{}, nil, nil, nil, nil, newSecretScanRejectionProblem(secretFindings)
	}

	// Filter --only and --exclude against workload name. Deterministic
	// order preserved (reposcan already sorts). The Intersection
	// check (--only ∩ --exclude = ∅) was already enforced pre-scan
	// after parseScanMultipart returns; the unknown-slug validation
	// runs here, post-scan, against the resolved scan workload set.
	var (
		filteredW  []reposcan.Workload
		filteredMc []reposcan.Managed
	)
	for _, wl := range result.Workloads {
		lname := strings.ToLower(wl.Name)
		if len(req.Only) > 0 && !req.Only[lname] {
			continue
		}
		// ADR-124: --exclude drops the workload from filteredW (and
		// therefore from reconcile + builds). The visibility row
		// still surfaces in resp.Skipped via computeAffectedPartition
		// running over result.Workloads.
		if len(req.Exclude) > 0 && req.Exclude[lname] {
			continue
		}
		filteredW = append(filteredW, wl)
	}
	// Post-scan exclude-validity check: every entry in req.Exclude
	// must correspond to a real scan workload OR an existing app in
	// the account. A typo would otherwise silently survive and
	// confuse the operator about what was honoured. 400
	// exclude_unknown_slug codes the failure with the unknown slug in
	// the message so the dashboard can render it inline.
	//
	// Existing-app slugs are accepted because operators commonly
	// exclude workloads that exist in the account but were renamed
	// or removed from the current commit (the audit fix at
	// pkg/reconcile/reconcile_test.go::TestReconcile_ExcludePreventsRemove
	// pins the apply-side contract). The Skipped partition below
	// surfaces excluded existing apps in BOTH Unaffected (blast-
	// radius) and Skipped (operator intent) views — see
	// TestScanPartition_ExcludedExistingAppDualView.
	//
	// Code-review fix #2: split persisted-vs-per-deploy
	// slugs in the unknown set. A persisted slug that no
	// longer exists in the repo (the workload was renamed or
	// deleted in a future commit) MUST NOT trip
	// exclude_unknown_slug — otherwise the operator is
	// locked out of subsequent deploys until they run
	// `gregale deployments exclude clear --slug=...` or
	// wait for the 90-day janitor to reap the row. Surface
	// these as StalePersistedExclusions instead; the dashboard
	// can render a "persisted exclusion ignored" badge.
	//
	// Load acctApps early so the exclude-validity check can
	// recognise existing-app slugs alongside scanNames. The same
	// `acctApps` slice is reused by computeAffectedPartition
	// below (Unaffected + Skipped + Removed loops) — one DB
	// round-trip per scan.
	acctApps, listErr := s.store.ListApps(r.Context(), acct.ID)
	if listErr != nil {
		return nil, state.Project{}, nil, nil, nil, nil, api.ErrInternal(
			fmt.Sprintf("list account apps: %v", listErr))
	}
	var stalePersistedSlugs []string
	if len(req.Exclude) > 0 {
		scanNames := make(map[string]bool, len(result.Workloads))
		for _, w := range result.Workloads {
			scanNames[strings.ToLower(w.Name)] = true
		}
		existingSlugs := make(map[string]bool, len(acctApps))
		for _, a := range acctApps {
			existingSlugs[strings.ToLower(a.Slug)] = true
		}
		var unknown []string
		if len(persistedSlugs) > 0 {
			persistedSet := make(map[string]bool, len(persistedSlugs))
			for _, slug := range persistedSlugs {
				persistedSet[slug] = true
			}
			for slug := range req.Exclude {
				if scanNames[slug] || existingSlugs[slug] {
					continue
				}
				if persistedSet[slug] {
					stalePersistedSlugs = append(stalePersistedSlugs, slug)
					delete(req.Exclude, slug)
				} else {
					unknown = append(unknown, slug)
				}
			}
		} else {
			for slug := range req.Exclude {
				if scanNames[slug] || existingSlugs[slug] {
					continue
				}
				unknown = append(unknown, slug)
			}
		}
		sort.Strings(stalePersistedSlugs)
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return nil, state.Project{}, nil, nil, nil, nil, api.NewProblem(
				http.StatusBadRequest, "exclude_unknown_slug",
				"exclude slug is not a workload in this commit",
				fmt.Sprintf("unknown: %s", strings.Join(unknown, ", ")))
		}
		// Drop stale persisted slugs from persistedSlugs so
		// the response.PersistedExclusions surface reflects
		// only the actually-applied ones (the audit emit and
		// the apply-side persist-write loop both key off
		// this slice).
		if len(stalePersistedSlugs) > 0 {
			staleSet := make(map[string]bool, len(stalePersistedSlugs))
			for _, slug := range stalePersistedSlugs {
				staleSet[slug] = true
			}
			filtered := persistedSlugs[:0]
			for _, slug := range persistedSlugs {
				if !staleSet[slug] {
					filtered = append(filtered, slug)
				}
			}
			persistedSlugs = filtered
		}
	}
	// Managed services (compose image: db, etc.) are not subject to
	// --only — the customer sees them in the plan either way so they
	// know what we're NOT provisioning. That mirrors the §4 fixture
	// (1 managed postgres).
	filteredMc = append(filteredMc, result.Managed...)

	// Crons: any workload with a Schedule string is also a cron. Map
	// to planCron with the workload name; resolve AppID in the apply
	// path from the just-inserted apps.
	var crons []planCron
	for _, wl := range filteredW {
		if wl.Schedule == "" {
			continue
		}
		path := wl.Ports // unused but keeps govet quiet
		_ = path
		crons = append(crons, planCron{
			WorkloadName: wl.Name,
			Schedule:     wl.Schedule,
			Path:         "/",
			Enabled:      true,
		})
	}

	// can_apply computation: apps + crons must fit under the plan
	// caps AND crons must be allowed. We mirror store.ApplyProjectPlan's
	// pre-check (no Tx here — the store is authoritative).
	observedApps, appCountErr := s.store.CountDeployedApps(r.Context(), acct.ID)
	if appCountErr != nil {
		return nil, state.Project{}, nil, nil, nil, nil, api.ErrInternal(
			fmt.Sprintf("count apps: %v", appCountErr))
	}
	observedCrons := countAccountCrons(r.Context(), s, acct.ID)

	var (
		canApply   bool
		notAllowed bool
		reasons    []string
	)
	// ADR-124 can_apply rescue signal: evaluate the gate twice.
	// preExclude uses result.Workloads (post-`--only`, pre-`--exclude`),
	// the same set the operator would have scanned without
	// excluding anything. canApply uses filteredW (post-`--only`
	// AND post-`--exclude`). The diff surfaces the rescue: a
	// blocked gate flipped to allowed only because --exclude
	// shrunk the workload set below the plan cap. evaluateQuotaGate
	// derives the cron count from workloads internally so both
	// calls are self-contained (no shared scan state).
	preCanApply, preNotAllowed, preReasons, _ := evaluateQuotaGate(result.Workloads, limits, observedApps, observedCrons)
	canApply, notAllowed, reasons, _ = evaluateQuotaGate(filteredW, limits, observedApps, observedCrons)

	// gateRescuedByExclude fires the slog seam when --exclude
	// flipped a blocked gate to allowed. ADR-124 follow-up #2
	// adds a parallel metric emission alongside the slog; both
	// are observable in production (slog for human triage, the
	// counter for dashboarding + alerting). The reason label
	// comes from gateRescueReason which collapses the templated
	// raw reason to a closed-set vocabulary (12 series total —
	// 4 plans × 3 reasons; pre-instantiated). s.ops may be nil
	// in unit tests; the nil-safe accessor returns a nil
	// counter and we skip the Inc(). This matches the
	// GuestTailFailedTotal caller pattern in cmd/schedd/main.go.
	gateRescuedByExclude := !preCanApply && canApply
	if gateRescuedByExclude {
		// Code-review fix #4: preReasons (the gate-failure
		// reasons that fired BEFORE --exclude shrank the
		// workload set) are the operationally meaningful
		// signal here — the POST-filter `reasons` are empty by
		// construction when canApply=true (the gate passed).
		// Passing post-filter reasons caused the metric to
		// always emit reason="unknown" on a rescue, defeating
		// the closed-set cardinality budget.
		s.log.Info("plan_gate_rescued_by_exclude",
			slog.String("project_slug", req.ProjectSlug),
			slog.String("account_id", acct.ID),
			slog.Bool("pre_exclude_can_apply", preCanApply),
			slog.Bool("post_exclude_can_apply", canApply),
			slog.Bool("pre_exclude_not_allowed", preNotAllowed),
			slog.Any("reasons", preReasons),
		)
		emitGateRescueMetric(s.ops, string(acct.Plan), preReasons)
	}

	// ADR-124 blast-radius partition: `acctApps` was loaded earlier
	// (above the exclude-validity check) so the same round-trip
	// serves both purposes. ListApps is account-scoped; per-account
	// apps include rows in other projects (intentional — Unaffected
	// is the blast-radius view, project-agnostic).

	// Load the project so the partition's Removed loop is project-
	// scoped (matches what reconcile will SoftDeleteAppCascade).
	// ErrNotFound is fine — brand-new project means no apps to
	// remove and Removed is the empty slice. Other errors fail
	// the scan (an unreadable project means the rest of the
	// scan path's project-aware decisions would also be wrong).
	var projectID string
	if proj, projErr := s.store.ProjectBySlug(r.Context(), acct.ID, req.ProjectSlug); projErr == nil {
		projectID = proj.ID
	} else if !errors.Is(projErr, state.ErrNotFound) {
		return nil, state.Project{}, nil, nil, nil, nil, api.ErrInternal(
			fmt.Sprintf("load project for partition: %v", projErr))
	}

	partition := computeAffectedPartition(filteredW, result.Workloads, acctApps, req.Exclude, projectID)

	// Emit one project.workload.skipped audit row per operator
	// --exclude entry (SOC 2 CC7.2 "who deployed v3 and what did
	// they skip?"). Preview-time emission is the source of truth;
	// the apply path runs the same partition so re-emitting there
	// would double-count. `partition.Skipped` rows are scan workloads
	// (`reposcan.Workload`s emitted by the scanner), NOT apps —
	// `row.Slug` IS the scan workload's `Name`. For a brand-new
	// excluded workload (the common case where no app with that
	// Slug exists yet), `row.ID == ""` and `row.Slug` carries the
	// workload name we need to stamp the audit row. Earlier code
	// looked `slugToApp[row.Slug]` in the app table and silently
	// skipped brand-new excludes — fixing the SOC 2 trail gap.
	actor := resolvedActorString(routeKindForRequest(r), acct.ID, "")
	sourceSHA := req.SourceSHA256
	emitSkippedAuditRows(r.Context(), s.audit, actor, acct.ID, projectID, sourceSHA, partition.Skipped)

	// Code-review fix #5: emit one project.scope.excluded audit
	// row per carried-forward persisted slug, tagged with
	// reason="persisted" so operators can trace which slugs
	// came from persistence vs the operator's per-deploy
	// --exclude. Without this row, the SOC 2 trail showed the
	// skip but couldn't tell you whether the operator typed
	// it today or whether it was the long-haul persisted
	// intent from a prior deploy. Preview-time emission is the
	// source of truth (same precedent as
	// emitSkippedAuditRows); the apply-side persist-write
	// handler emits an additional row tagged
	// reason="persisted_via_flag" for the write itself.
	if len(persistedSlugs) > 0 {
		emitPersistedExcludedAuditRows(r.Context(), s.audit, actor, acct.ID, projectID, sourceSHA, persistedSlugs)
	}

	// Convert the reposcan carrier slice into the wire-shape DTO so
	// the JSON marshal sees string Tier (matching OpenAPI enum +
	// pkg/api.PlanWorkload.Tier) instead of the raw int the
	// reposcan type carries. See toPlanWorkload / toPlanManaged.
	respWorkloads := make([]api.PlanWorkload, len(filteredW))
	for i, w := range filteredW {
		pw := toPlanWorkload(w)
		pw.Action = partition.WillDeploy[i].Action
		pw.ExistingAppID = partition.WillDeploy[i].ID
		respWorkloads[i] = pw
	}
	respManaged := make([]api.PlanManaged, len(filteredMc))
	for i, m := range filteredMc {
		respManaged[i] = toPlanManaged(m)
	}
	resp := &scanPlanResponse{
		ProjectSlug:   req.ProjectSlug,
		ScanSource:    reconcile.DeriveScanSource(filteredW),
		Tier:          result.Tier.String(),
		Workloads:     respWorkloads,
		Managed:       respManaged,
		Crons:         crons,
		Warnings:      result.Warnings,
		ObservedApps:  observedApps + len(filteredW),
		ObservedCrons: observedCrons + len(crons),
		LimitApps:     limits.DeployedApps,
		LimitCrons:    limits.CronLimitPerAccount,
		CanApply:      canApply,
		NotAllowed:    notAllowed,
		// ADR-124 can_apply rescue signal. PreExclude + Rescued
		// are the operator-facing knobs the dashboard renders in
		// the gate card; CanApplyReasons is the human-readable
		// post-exclude failure list (empty on success — omitempty
		// drops the wire shape).
		CanApplyPreExclude:   preCanApply,
		GateRescuedByExclude: gateRescuedByExclude,
		CanApplyReasons:      reasons,
		// ADR-124 follow-up #3 (PR-B commit 5): pass the operator's
		// persist intent through to the apply handler so it can
		// write the deployment_scope_exclusions rows on a
		// successful apply. The scan path accepts the field but
		// does nothing with it (default-OFF posture).
		PersistExclude: req.PersistExclude,
		// ADR-124 partition projection. Skipped is the operator --exclude
		// subset; Unaffected is every account app not in the scan keys;
		// WillDeploy keeps reposcan's order so the i-alignment with
		// respWorkloads (one row per post-`--only`/post-`--exclude`
		// workload) is preserved. Removed is the destructive subset —
		// populated from the partition on preview (project-scoped
		// apps whose key isn't in the scan set), and from removedSlugs
		// on apply (the canonical reconcile-engine result overrides
		// the partition projection below at the apply-path tail).
		WillDeploy: partition.WillDeploy,
		Unaffected: partition.Unaffected,
		Skipped:    partition.Skipped,
		Removed:    partition.Removed,
		// ADR-124 follow-up #3 — persisted_exclusions mirrors the
		// slugs the apply path folded in from the
		// deployment_scope_exclusions table (when req.Exclude was
		// empty). Empty when no persisted rows exist or when the
		// scan path is preview; the omitempty keeps --json output
		// stable for the common case.
		PersistedExclusions: persistedSlugs,
		// Code-review fix #2: stale_persisted_exclusions is the
		// subset of PersistedExclusions whose slug is no longer
		// in the repo. Surfaced so the dashboard can render a
		// "persisted exclusion ignored" badge instead of failing
		// the deploy with exclude_unknown_slug.
		StalePersistedExclusions: stalePersistedSlugs,
	}

	// Mint a fresh plan_token unless one was supplied (apply path
	// keeps the caller's; minting a new one would be confusing).
	if planToken == "" {
		tok, mintErr := mintPlanToken(acct.ID, req.ProjectSlug, req.SourceSHA256)
		if mintErr != nil {
			return nil, state.Project{}, nil, nil, nil, nil, api.ErrInternal(
				fmt.Sprintf("mint plan_token: %v", mintErr))
		}
		resp.PlanToken = tok
	} else {
		resp.PlanToken = planToken
	}

	// ADR-124: cache the source under its SHA-256 so the dashboard
	// apply handler can replay it without re-uploading (browsers
	// strip file inputs from non-multipart submissions; the operator
	// never re-attaches the tarball). The defer at the top of
	// scanService still removes the original spool file — that
	// cleanup runs AFTER this return, so the cache copy observes a
	// live source. Best-effort: a copy failure is logged (in
	// storePlanCache via the returned error) and the dashboard apply
	// path falls back to "please re-upload". We do NOT fail the
	// scan on cache miss because the CLI flow doesn't need it.
	// Plan-token mint already happened; the cache key is the same
	// SHA-256 the token binds to.
	if cacheErr := storePlanCache(req.SourceSHA256, req.SourcePath, acct.ID); cacheErr != nil {
		s.log.Warn("plan_source cache store failed", slog.String("err", cacheErr.Error()))
	}

	if !apply {
		return resp, state.Project{}, nil, nil, nil, nil, nil
	}

	// Apply path (PR-G, repo decomposition Phase 5): route every

	// Apply path (PR-G, repo decomposition Phase 5): route every
	// workload-mutating action through pkg/reconcile.Service. The
	// scan-service no longer builds state.App rows directly; the
	// reconcile package owns the diff/apply contract and emits the
	// audit rows. Pre-PR-G this branch built stateApps + stateCrons
	// and handed them to store.ApplyProjectPlan; post-PR-G we hand
	// state.Project + reposcan.Result to reconcileSvc.Reconcile.
	if !canApply {
		// Don't even call Reconcile — quota check is reconcile's
		// job but the handler routes the right HTTP code based on
		// the resp flags. Return a sentinel problem so the handler
		// can branch on canApply=false without parsing.
		var prob *api.Problem
		switch {
		case notAllowed:
			prob = api.ErrPlanCronsNotAllowed(acct.Plan)
		case observedApps+len(filteredW) > limits.DeployedApps:
			prob = api.ErrPlanLimitApps(limits, observedApps+len(filteredW))
		default:
			prob = api.ErrPlanCronQuota(acct.Plan, "account",
				limits.CronLimitPerAccount, observedCrons+len(crons))
		}
		return resp, state.Project{}, nil, nil, nil, nil, prob
	}

	project := state.Project{
		AccountID:        acct.ID,
		Slug:             req.ProjectSlug,
		RepoFullName:     "",
		ProductionBranch: req.ProdBranch,
		InstallID:        req.InstallID,
		ScanSource:       resp.ScanSource,
	}
	// CreateProject inserts the projects row and stamps ID +
	// CreatedAt. This MUST happen before reconcile runs — the
	// reconcile path's CreateAppIfUnderQuota cascades a
	// project_id FK (apps.project_id → projects.id), and a NULL
	// project_id would skip the apps_project_workload_uniq
	// enforcement path. Pre-PR-G, store.ApplyProjectPlan inserted
	// project + apps in one Tx; the split here is the cost of
	// the package boundary (pkg/reconcile never imports pkg/state
	// types beyond the Store interface).
	//
	// Atomicity (PR-GH.6 review H9 fix): if the subsequent
	// reconcile fails, the project row is rolled back via
	// store.DeleteProject. apps.project_id is declared ON
	// DELETE SET NULL (migration 00074:74), so any apps
	// reconcile already inserted (per-row Tx) have their FK
	// nulled — they stay durable (audit chain) but no longer
	// appear under any project. The rollback is best-effort:
	// a DeleteProject failure logs but does not mask the
	// reconcile error the caller is returning.
	// Upsert-by-slug (ADR-068 amendment, post-merge review):
	// POST /v1/projects is idempotent on (account_id, slug). A
	// second apply with the same slug re-uses the existing project
	// row so reconcile can diff the workloads (adds / changes /
	// soft-deletes). Without upsert, every diff test (re-apply
	// same body to assert no-op) trips projects_account_slug_uniq
	// and returns 409.
	//
	// Production branch / install_id / scan_source are kept from
	// the original row — the request's values are applied only on
	// the first insert; re-applies leave them alone. Customers
	// change those via the dashboard, not by re-applying.
	//
	// Race window: two concurrent applies with the same slug both
	// miss ProjectBySlug, both call CreateProject, one wins and the
	// loser hits ErrConflict → we re-load and proceed. Two-step
	// (read-then-insert) is cheaper than ON CONFLICT DO NOTHING +
	// RETURNING + fallback SELECT, and the ErrConflict→reload path
	// is rare.
	var projectCreated bool
	existing, lookupErr := s.store.ProjectBySlug(r.Context(), acct.ID, req.ProjectSlug)
	switch {
	case lookupErr == nil:
		// Reuse existing row. Don't rollback if reconcile fails —
		// the project pre-dated this apply.
		project = existing
		projectCreated = false
	case errors.Is(lookupErr, state.ErrNotFound):
		created, projErr := s.store.CreateProject(r.Context(), project)
		if projErr != nil {
			if errors.Is(projErr, state.ErrConflict) {
				// Lost a race against a concurrent apply — the
				// winner just inserted; re-load and reuse.
				if reloaded, reloadErr := s.store.ProjectBySlug(r.Context(), acct.ID, req.ProjectSlug); reloadErr == nil {
					project = reloaded
					projectCreated = false
				} else {
					prob := api.NewProblem(http.StatusConflict,
						api.CodeValidation, "Project slug collision",
						"this project slug is already taken")
					return resp, state.Project{}, nil, nil, nil, nil, prob
				}
			} else if errors.Is(projErr, state.ErrNotFound) {
				prob := api.NewProblem(http.StatusNotFound,
					api.CodeValidation, "Account not found", "")
				return resp, state.Project{}, nil, nil, nil, nil, prob
			} else {
				prob := api.ErrInternal(fmt.Sprintf("create project: %v", projErr))
				return resp, state.Project{}, nil, nil, nil, nil, prob
			}
		} else {
			project = created
			projectCreated = true
		}
	default:
		prob := api.ErrInternal(fmt.Sprintf("load existing project: %v", lookupErr))
		return resp, state.Project{}, nil, nil, nil, nil, prob
	}
	// Defer project rollback for any error path below.
	// capturedProb tracks whether we already wrapped the
	// reconcile error; the defer fires BEFORE the return
	// statement so rollback runs first.
	//
	// rollbackCtx is captured explicitly so the defer closure
	// doesn't extend the lifetime of r (contextcheck linter).
	// projectCreated gates the rollback: we only delete a project
	// that THIS request inserted. Re-applying an existing project
	// is upsert-by-slug (ADR-068 amendment); a reconcile failure
	// there must not DeleteProject the customer's pre-existing
	// row, which would orphan any apps the customer has since
	// provisioned through the dashboard.
	rollbackCtx := r.Context()
	var capturedProb *api.Problem
	defer func() {
		if capturedProb != nil && projectCreated && project.ID != "" {
			if dErr := s.store.DeleteProject(rollbackCtx, project.ID); dErr != nil {
				// Best-effort: a DeleteProject failure doesn't
				// mask the underlying reconcile error the
				// caller is returning. The DeleteProject's own
				// ErrNotFound is fine (concurrent delete raced).
				if !errors.Is(dErr, state.ErrNotFound) {
					s.log.Warn("apid: rollback project on reconcile error",
						"project_id", project.ID, "err", dErr)
				}
			}
		}
	}()

	// Build the cron name list (handler uses index parity to look up
	// the AppID post-reconcile). Order is the same as the scan order
	// (reposcan sorts by Name), and reconcile's workloadToDraftApp
	// preserves that order in Result.Added — so the handler's
	// resp.CronNames[i] ↔ Result.Added/Changed slug lookup is safe.
	for i := range crons {
		resp.CronNames = append(resp.CronNames, crons[i].WorkloadName)
	}

	// Hand off to reconcile.Service. The post-Reconcile Result
	// carries Added (creates), Changed (updates), Removed (soft-
	// deletes), and Alerts (guard-tripped notifications). The
	// handler reads Added + Changed to build the slug→ID map for
	// cron stamping and to emit per-app NotifyAppChanged.
	//
	// The reposcan.Result handed to reconcile is the --only-filtered
	// subset, mirroring the legacy stateApps construction at the top
	// of this branch (filteredW). The tier + warnings + managed
	// metadata pass through verbatim — only Workloads is filtered.
	filteredScan := reposcan.Result{
		Workloads: filteredW,
		Managed:   filteredMc,
		Tier:      result.Tier,
		Warnings:  result.Warnings,
	}

	// preRemoveIdToSlug maps each app's ID to its pre-remove slug.
	// Built BEFORE reconcile runs because reconcile's applyRemove
	// calls SoftDeleteAppCascade (which sets status='deleted'),
	// and AppsForProject filters `status<>'deleted'`
	// (pgstore.go:3593, memstore.go:2061) — a post-reconcile read
	// would miss every just-removed app. Code-review finding #2:
	// the earlier version called AppsForProject AFTER reconcile,
	// so resp.Removed was always empty even when reconcile
	// correctly soft-deleted the apps. The dashboard ApplyResult
	// banner + CLI `faas apply` JSON both lied to operators about
	// what was deleted.
	preRemoveApps, preLoadErr := s.store.AppsForProject(r.Context(), acct.ID, project.ID)
	if preLoadErr != nil {
		prob := api.ErrInternal(fmt.Sprintf("load existing apps: %v", preLoadErr))
		capturedProb = prob
		return resp, state.Project{}, nil, nil, nil, nil, prob
	}
	preRemoveIdToSlug := make(map[string]string, len(preRemoveApps))
	for _, a := range preRemoveApps {
		preRemoveIdToSlug[a.ID] = a.Slug
	}

	reconcileInputs := toReconcileInputs(*req, project, filteredScan)
	// req.Exclude is map[string]bool (slug-set on the wire
	// boundary); reconcile.Service.Reconcile takes []string.
	// Convert at the call site so the engine signature stays
	// ordered/dupe-tolerant (the wire side is a set, the engine
	// is a list — pkg/reconcile dedupes via the map filter).
	excludeList := make([]string, 0, len(req.Exclude))
	for slug := range req.Exclude {
		excludeList = append(excludeList, slug)
	}
	rec, recErr := s.reconcileSvc.Reconcile(
		r.Context(), project, filteredScan,
		reconcileInputs.CommitSHA, reconcileInputs.Branch,
		excludeList)
	if recErr != nil {
		// Map reconcile-package errors into the existing RFC 7807
		// problem shapes so the handler can use a single dispatch
		// path. mapReconcileError returns nil for nil err, so the
		// caller can guard on prob != nil.
		mapped := mapReconcileError(recErr)
		if mapped != nil {
			var prob *api.Problem
			if mapped.Quota != nil {
				prob = quotaProblem(acct.Plan, limits, mapped.Quota)
			} else {
				prob = api.NewProblem(mapped.Status, mapped.Code, mapped.Msg, "")
			}
			capturedProb = prob
			return resp, state.Project{}, nil, nil, nil, nil, prob
		}
		// mapReconcileError returned nil for nil err — unreachable
		// here, but defensively pass through as 500.
		prob := api.ErrInternal(fmt.Sprintf("reconcile: %v", recErr))
		capturedProb = prob
		return resp, state.Project{}, nil, nil, nil, nil, prob
	}

	// Compute removed slugs. rec.Removed is a list of IDs; the
	// handler needs slugs to soft-delete the corresponding crons
	// (workloads dropped from the scan no longer appear in
	// resp.Crons so the slug→ID lookup in handlers_decompose.go
	// returns empty — without removedSlugs the handler would 500
	// on a removed workload that previously had a cron). The
	// `preRemoveIdToSlug` map is built BEFORE reconcile (see
	// above); here we just resolve each removed ID through it.
	removedSlugs := make([]string, 0, len(rec.Removed))
	for _, id := range rec.Removed {
		if slug, ok := preRemoveIdToSlug[id]; ok {
			removedSlugs = append(removedSlugs, slug)
		}
	}

	// Fold the reconcile Result into the legacy return shape. The
	// handler reads the third slot (added) and fourth slot
	// (changed) to:
	//
	//  - build the slug→ID map for cron stamping (added ∪ changed)
	//  - emit per-app NotifyAppChanged with kind=created for
	//    rec.Added entries and kind=updated for rec.Changed entries
	//  - soft-delete crons whose workload_name is in removedSlugs
	//
	// Each slice carries post-insert/post-update rows with valid
	// IDs (reconcile's CreateAppIfUnderQuota stamps ID + CreatedAt;
	// UpdateApp stamps UpdatedAt).
	//
	// Apply-time build enqueue (PR-A, repo decomposition Phase 5
	// close-the-loop): for every added + changed app, stage a
	// per-workload tarball from req.ScanDir and call
	// apidsource.Enqueue. Partial failure is by design — see
	// applyBuildsForAddedChanged for the rationale. The handler
	// renders the results in the apply response so the CLI's
	// `faas apply` flow can show "app X: deployment Y, build Z".
	//
	// ScanDir lifetime: this function reads from req.ScanDir,
	// which is cleaned up by the handler's defer on req.ScanDir
	// (registered above). Defers fire in reverse order, so the
	// handler's defer runs AFTER this function returns — meaning
	// req.ScanDir is still on disk while staging reads it, which
	// is what we want. A future refactor that moves the ScanDir
	// cleanup into this func must keep staging reads ahead of
	// the cleanup.
	builds := s.applyBuildsForAddedChanged(r.Context(), r, acct, project, req.ScanDir, rec.Added, rec.Changed)
	// ADR-124: surface the destructive subset on the response so the
	// apply handler can render Removed in the same blast-radius
	// envelope the preview offered. SoftDeleteAppCascade runs
	// inside applyActions (pkg/reconcile/apply.go:242-251), already
	// audited and idempotent — this is just the wire projection.
	resp.Removed = removedSlugs
	return resp, project, rec.Added, rec.Changed, removedSlugs, builds, nil
}

// parseScanMultipart reads the multipart body, spools, validates, and
// extracts the tarball. On success, req.SourcePath points at the
// original tarball (cleaned up by the caller), req.ScanDir at the
// extracted root (cleaned up by the caller), and req.SourceSHA256 at
// the hex digest of the compressed bytes.
func parseScanMultipart(r *http.Request, acct state.Account, limits api.Limits) (*scanPlanRequest, *api.Problem) {
	// Multipart cap before parsing — mirrors createDeployment.
	max := int64(limits.SourceTarballMaxMB) * 1024 * 1024
	r.Body = http.MaxBytesReader(nil, r.Body, max)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad multipart", err.Error())
	}
	var (
		sourcePath     string
		onlySet        = map[string]bool{}
		excludeSet     = map[string]bool{}
		projectSlug    string
		prodBranch     = "main"
		installID      int64
		persistExclude bool
	)
	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			return nil, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Bad multipart", perr.Error())
		}
		name := part.FormName()
		switch name {
		case "source":
			if vErr := assertMultipartFileName(part); vErr != nil {
				return nil, vErr
			}
			path, n, vErr := validateAndSpool(part, limits)
			if vErr != nil {
				return nil, vErr
			}
			sourcePath = path
			_ = n
		case "project_slug":
			b, _ := io.ReadAll(io.LimitReader(part, 64))
			projectSlug = strings.TrimSpace(string(b))
		case "production_branch":
			b, _ := io.ReadAll(io.LimitReader(part, 64))
			prodBranch = strings.TrimSpace(string(b))
			if prodBranch == "" {
				prodBranch = "main"
			}
		case "install_id":
			b, _ := io.ReadAll(io.LimitReader(part, 32))
			//nolint:errcheck // empty input → installID stays 0; the
			// apply handler treats 0 as "no install binding" (issue #313).
			_, _ = fmt.Sscanf(string(b), "%d", &installID)
		case "only":
			b, _ := io.ReadAll(io.LimitReader(part, 1024))
			for _, s := range strings.Split(string(b), ",") {
				s = strings.ToLower(strings.TrimSpace(s))
				if s != "" {
					onlySet[s] = true
				}
			}
		case "exclude":
			// ADR-124 inverse-allowlist. Same lowercased/trimmed
			// shape as `only` so the wire contract is symmetric.
			b, _ := io.ReadAll(io.LimitReader(part, 1024))
			for _, s := range strings.Split(string(b), ",") {
				s = strings.ToLower(strings.TrimSpace(s))
				if s != "" {
					excludeSet[s] = true
				}
			}
		case "persist_exclude":
			// ADR-124 follow-up #3 (PR-B commit 5): write-side
			// flag. The scan path accepts and ignores; the apply
			// path triggers CreateDeploymentScopeExclusion per
			// excluded slug on a successful apply. Empty value or
			// absent field = false (the default-OFF posture).
			b, _ := io.ReadAll(io.LimitReader(part, 32))
			v := strings.ToLower(strings.TrimSpace(string(b)))
			// strconv.ParseBool accepts "1", "t", "T", "TRUE",
			// "true", "True", "0", "f", "F", "FALSE", "false",
			// "False" — broader than the hand-rolled
			// {"true","1","yes"} set, and matches the convention
			// of the other 6 boolean fields in the package
			// (code-review finding #8).
			if v != "" {
				if parsed, perr := strconv.ParseBool(v); perr == nil {
					persistExclude = parsed
				}
			}
		default:
			_, _ = io.Copy(io.Discard, part)
		}
		_ = part.Close()
	}
	if sourcePath == "" {
		return nil, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Source required", "multipart applies require a 'source' file field")
	}
	if projectSlug == "" {
		// default to repo dir basename — but we don't have it here.
		// Fall back to a random placeholder; the handler can correct
		// if it has extra context (--repo on the CLI).
		projectSlug = "project-" + randomToken(6)
	}

	// Hash the spooled bytes BEFORE extract (extract consumes the
	// file handle). SHA-256 over the compressed bytes is the
	// plan_token's hash field.
	hash, hashErr := hashFileSHA256(sourcePath)
	if hashErr != nil {
		return nil, api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid,
			"Bad source", hashErr.Error())
	}

	// Extract to disk and validate the §11 hardening posture.
	lim := defaultExtractLimits(limits)
	scanDir, prob := extractTarGzToDir(sourcePath, lim)
	if prob != nil {
		return nil, prob
	}

	return &scanPlanRequest{
		SourcePath:     sourcePath,
		SourceSHA256:   hash,
		ScanDir:        scanDir,
		ProjectSlug:    projectSlug,
		ProdBranch:     prodBranch,
		InstallID:      installID,
		Only:           onlySet,
		Exclude:        excludeSet,
		PersistExclude: persistExclude,
	}, nil
}

// hashFileSHA256 streams the file through SHA-256 without loading
// the whole thing into memory. The cap is enforced by the
// MaxBytesReader above so this can't pin apid.
func hashFileSHA256(path string) (string, error) {
	//nolint:forbidigo // path is the daemon-spooled tarball from
	// FAAS_SCAN_SPOOL_ROOT (set by scanService); not customer input.
	// The lint tripwire for customer-path os.Open is in cmd/gregale.
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// mintPlanToken produces the base64-JSON blob. The hash is the
// SHA-256 of the source bytes (the apply handler re-hashes and
// compares). AccountID prevents token-reuse across accounts.
func mintPlanToken(accountID, slug, hashHex string) (string, error) {
	pt := planTokenWire{
		Hash:      hashHex,
		AccountID: accountID,
		Slug:      slug,
		TSUnix:    nowUnix(),
	}
	b, err := json.Marshal(pt)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// countAccountCrons returns the count of crons across the account's
// non-deleted apps. Mirrors the store-side COUNT in
// ApplyProjectPlan; the duplication exists because the scan service
// runs OUTSIDE the apply Tx so it needs its own count to render
// can_apply accurately. The store's count inside the Tx is the
// authoritative one — if the two disagree, the store wins on commit.
func countAccountCrons(ctx context.Context, s *server, accountID string) int {
	apps, err := s.store.ListApps(ctx, accountID)
	if err != nil {
		return 0
	}
	var total int
	for _, a := range apps {
		cs, err := s.store.ListCronsForApp(ctx, a.ID)
		if err != nil {
			continue
		}
		total += len(cs)
	}
	return total
}

// --- response side helpers --------------------------------------------------

// nowUnix returns the current time in seconds. The plan_token's TS
// field is informational — the hash is the load-bearing field —
// but exposing a function variable lets tests freeze the clock.
var nowUnix = func() int64 { return time.Now().Unix() }
