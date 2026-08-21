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
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
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
	PlanToken     string   `json:"plan_token"`
	// ADR-124 blast-radius partition. WillDeploy + Unaffected
	// enumerate the scan-workload existence against every non-deleted
	// app in the account keyed by (RootDir, Name). Skipped is the
	// operator --exclude subset of WillDeploy (Stage 4). Removed is
	// populated only on the apply path from rec.Removed (the apps
	// reconcile will SoftDeleteAppCascade). See
	// computeAffectedPartition for the partition rule.
	WillDeploy []api.PlanAffectedApp `json:"will_deploy,omitempty"`
	Unaffected []api.PlanAffectedApp `json:"unaffected,omitempty"`
	Skipped    []api.PlanAffectedApp `json:"skipped,omitempty"`
	Removed    []string              `json:"removed,omitempty"`
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
// Removed is empty for preview (apply=false); the apply path
// populates it from rec.Removed at the post-reconcile site. This
// asymmetry is intentional: a preview that hard-predicts project
// deletions is wrong on the first ever commit (no project yet).
type affectedPartition struct {
	WillDeploy []api.PlanAffectedApp
	Unaffected []api.PlanAffectedApp
	// Skipped is the operator --exclude subset of scan workloads.
	// Visible on the dashboard as "excluded by operator". Action
	// is always "noop" — the apply path skips these entirely.
	Skipped []api.PlanAffectedApp
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
//	"remove" — populated post-reconcile from rec.Removed (apply path).
func computeAffectedPartition(
	filteredW []reposcan.Workload,
	allScanWl []reposcan.Workload,
	existingApps []state.App,
	exclude map[string]bool,
) affectedPartition {
	idx := make(map[workloadKey]state.App, len(existingApps))
	for _, a := range existingApps {
		idx[workloadKey{RootDir: a.RootDir, Name: a.WorkloadName}] = a
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
	// whose lowercased name is in exclude. Order preserved.
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
	}
	// Unaffected: existing apps whose (RootDir, Name) is not in any
	// scan workload. The "no scan workload" check is across allScanWl
	// (post-`--only`) so an excluded update doesn't shift an app from
	// Unaffected to Skipped — it stays in Skipped only.
	scanKeys := make(map[workloadKey]struct{}, len(allScanWl))
	for _, w := range allScanWl {
		scanKeys[workloadKey{RootDir: w.RootDir, Name: w.Name}] = struct{}{}
	}
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
	return affectedPartition{
		WillDeploy: will,
		Unaffected: unaff,
		Skipped:    skip,
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
	// when the customer wasn't going to apply. The scan is non-fatal
	// to reposcan: a walk error is logged via the envelope's detail
	// but findings drive the 422. The walk is bounded by
	// serverSecretScanMaxBytes (1 MiB per file) +
	// serverSecretScanExcludeDirs (.git, node_modules, vendor, …)
	// so a 10k-file customer tree completes in p95 ≤ 250 ms (target
	// envelope; not pinned by spec).
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
	// must correspond to a real scan workload. A typo would otherwise
	// silently survive and confuse the operator about what was
	// honoured. 400 exclude_unknown_slug codes the failure with the
	// unknown slug in the message so the dashboard can render it
	// inline.
	if len(req.Exclude) > 0 {
		scanNames := make(map[string]bool, len(result.Workloads))
		for _, w := range result.Workloads {
			scanNames[strings.ToLower(w.Name)] = true
		}
		var unknown []string
		for slug := range req.Exclude {
			if !scanNames[slug] {
				unknown = append(unknown, slug)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return nil, state.Project{}, nil, nil, nil, nil, api.NewProblem(
				http.StatusBadRequest, "exclude_unknown_slug",
				"exclude slug is not a workload in this commit",
				fmt.Sprintf("unknown: %s", strings.Join(unknown, ", ")))
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

	canApply := true
	notAllowed := false
	if observedApps+len(filteredW) > limits.DeployedApps {
		canApply = false
	}
	if len(crons) > 0 && limits.CronLimitPerAccount == 0 {
		canApply = false
		notAllowed = true
	}
	if observedCrons+len(crons) > limits.CronLimitPerAccount {
		canApply = false
	}

	// ADR-124 blast-radius partition: load every non-deleted app in
	// the account and project each (RootDir, Name) against the scan
	// workload set. ListApps is account-scoped; per-account apps
	// include rows in other projects (intentional — Unaffected is
	// the blast-radius view, project-agnostic).
	acctApps, listErr := s.store.ListApps(r.Context(), acct.ID)
	if listErr != nil {
		return nil, state.Project{}, nil, nil, nil, nil, api.ErrInternal(
			fmt.Sprintf("list account apps: %v", listErr))
	}
	partition := computeAffectedPartition(filteredW, result.Workloads, acctApps, req.Exclude)

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
		// ADR-124 partition projection. Skipped is the operator --exclude
		// subset; Unaffected is every account app not in the scan keys;
		// WillDeploy keeps reposcan's order so the i-alignment with
		// respWorkloads (one row per post-`--only`/post-`--exclude`
		// workload) is preserved. Removed is empty on preview (apply=false);
		// the apply path populates it from removedSlugs.
		WillDeploy: partition.WillDeploy,
		Unaffected: partition.Unaffected,
		Skipped:    partition.Skipped,
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
	reconcileInputs := toReconcileInputs(*req, project, filteredScan)
	rec, recErr := s.reconcileSvc.Reconcile(
		r.Context(), project, filteredScan,
		reconcileInputs.CommitSHA, reconcileInputs.Branch)
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
	// on a removed workload that previously had a cron). We
	// resolve slug from the (now-deleted) rec.Added ∪ rec.Changed
	// maps via the inverse map below.
	removedSlugs := make([]string, 0, len(rec.Removed))
	if len(rec.Removed) > 0 {
		// removedSlugByID is keyed by the pre-remove app ID.
		// Build it BEFORE reconcile so we capture the slug of
		// every app that the scan dropped. The state.App rows
		// we look at are the project's pre-reconcile member
		// list (read-only); reconcile's removal happens
		// downstream of this lookup.
		existingApps, lerr := s.store.AppsForProject(r.Context(), acct.ID, project.ID)
		if lerr != nil {
			prob := api.ErrInternal(fmt.Sprintf("load existing apps: %v", lerr))
			capturedProb = prob
			return resp, state.Project{}, nil, nil, nil, nil, prob
		}
		idToSlug := make(map[string]string, len(existingApps))
		for _, a := range existingApps {
			idToSlug[a.ID] = a.Slug
		}
		for _, id := range rec.Removed {
			if slug, ok := idToSlug[id]; ok {
				removedSlugs = append(removedSlugs, slug)
			}
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
		sourcePath  string
		onlySet     = map[string]bool{}
		excludeSet  = map[string]bool{}
		projectSlug string
		prodBranch  = "main"
		installID   int64
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
		SourcePath:   sourcePath,
		SourceSHA256: hash,
		ScanDir:      scanDir,
		ProjectSlug:  projectSlug,
		ProdBranch:   prodBranch,
		InstallID:    installID,
		Only:         onlySet,
		Exclude:      excludeSet,
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
