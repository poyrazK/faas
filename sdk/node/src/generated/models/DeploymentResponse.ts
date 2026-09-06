/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BuildPlan } from './BuildPlan.js';
import type { DeploymentHealthcheck } from './DeploymentHealthcheck.js';
import type { DeploymentLivenessProbe } from './DeploymentLivenessProbe.js';
import type { LogExcerpt } from './LogExcerpt.js';
import type { ScanResult } from './ScanResult.js';
import type { SecretScanResult } from './SecretScanResult.js';
/**
 * One deployment: id, app, source ref, build status, commit SHA, and lifecycle timestamps. The optional `has_overrides` and `override_*` fields are the persisted echo of the create-time overrides object (issue #460 / ADR-053); they round-trip via `GET /v1/apps/{slug}/deployments/{id}` so a customer can audit what their last deploy pinned. Env values are NEVER echoed — only the keys (`override_env_keys`); env_secrets refs ARE echoed because the ref shape is non-secret by design.
 */
export type DeploymentResponse = {
  id: string;
  app_id: string;
  build_id?: string | null;
  image_digest: string;
  kind: string;
  /**
   * Deployment lifecycle status. `pending`, `building`, `imaging` and
   * `snapshotting` are in flight; `live` is serving traffic;
   * `superseded` shipped and was replaced by a newer deployment;
   * `failed` and `cancelled` are terminal exits (cancel is the
   * user-driven retract of a non-live row, ADR-124).
   *
   */
  status: 'pending' | 'building' | 'imaging' | 'snapshotting' | 'live' | 'failed' | 'superseded' | 'cancelled';
  error?: string | null;
  error_code?: string | null;
  /**
   * One-line next-action lifted from pkg/whycopy catalog.
   */
  error_hint?: string | null;
  /**
   * Human-readable cause with observed value.
   */
  error_why?: string | null;
  /**
   * Prescriptive remediation (1-3 lines).
   */
  error_fix?: string | null;
  /**
   * Per-line log excerpts explaining the failure (error-explanations cluster). Capped at 20 entries × 512 bytes by the CLI tripwire.
   */
  error_relevant_logs?: Array<LogExcerpt>;
  created_at: string;
  /**
   * Repository-relative build root used by a workspace context upload; omitted when the archive root is built.
   */
  source_root?: string;
  /**
   * True when this deployment carries a non-null override_* column set.
   */
  has_overrides?: boolean;
  /**
   * Entrypoint override echoed verbatim from the create request. nil when no override was supplied.
   */
  override_entrypoint?: Array<string>;
  /**
   * Cmd override echoed verbatim from the create request.
   */
  override_cmd?: Array<string>;
  /**
   * Sorted set of env-var keys set by the env override. VALUES ARE NEVER ECHOED (ADR-053 §Decision 4).
   */
  override_env_keys?: Array<string>;
  /**
   * Sorted set of env-var keys set by the env_secrets override. The parallel refs are echoed in `override_env_secret_refs` because the ref shape is non-secret by design.
   */
  override_env_secret_keys?: Array<string>;
  /**
   * Verbatim `secret:NAME` ref map; the customer needs to see which secret they bound to which env var to debug a misconfigured deploy.
   */
  override_env_secret_refs?: Record<string, string>;
  /**
   * Listen-port override; 0 = absent (fall back to image default).
   */
  override_port?: number;
  /**
   * Readiness-probe override. Persisted verbatim; the actual HTTP probe is a follow-up — today waitReady stays a bare TCP accept.
   */
  override_healthcheck?: (DeploymentHealthcheck | null);
  /**
   * Liveness-probe override echoed verbatim (issue #554 / ADR-078). nil when the deployment used the per-plan default (Hobby/Pro/Scale → 5s / 3 consecutive / 60s cooldown). Echoed on GET /v1/apps/{slug}/deployments/{id} so the customer can audit which probe the host (cmd/vmmd) is running against the VM.
   */
  override_liveness_probe?: (DeploymentLivenessProbe | null);
  /**
   * Per-deployment cold-wake floor override (issue #557 closure / ADR-072). 0 = inherit from parent app (default); positive value is the deployment's own floor. Effective per-instance floor = max(app.EffectiveMinInstances(), d.EffectiveMinInstances()). Validated against the parent app's plan MaxMinInstances cap on PATCH.
   */
  min_instances?: number;
  /**
   * Per-deploy grype CVE scan surface (issue #464 / ADR-055). nil on pre-feature rows (the migration backfilled scan_status='skipped' + scan_result={reason: 'pre-feature'} on those; the apid read path returns nil so the dashboard / CLI see a clean absence — the /scan route surfaces the 'skipped' sentinel for those rows). Non-nil for post-feature rows in any of the {pending, complete, failed, skipped} states. The customer can deploy a CRITICAL-CVE image; the dashboard shows it; that is the contract (no enforcement at the deploy gate).
   */
  scan?: (ScanResult | null);
  /**
   * Per-deployment parking reason (issue #554 / ADR-079 follow-up, migration 00157). Closed-set vocabulary enforced at the schema layer via the deployments_parked_reason_check constraint. nil for never-parked deployments — surfaced as no field on the wire via omitempty.
   */
  parked_reason?: 'liveness_exhausted' | 'lifecycle_park' | 'admin_park';
  /**
   * Wall-clock timestamp the deployment was parked (set once, idempotent across schedd restart cycles). nil for never-parked deployments.
   */
  parked_at?: string | null;
  /**
   * Per-deployment traffic-split weight (issue #556 PR-A). Summed across live rows for the app = 100 by construction.
   */
  traffic_percent?: number;
  /**
   * Per-deployment env scope (ADR-091 / PR-D). Lowercase alnum + dash, 3..40 chars, no leading/trailing dash. nil/omitted = `default`.
   */
  scope?: string | null;
  /**
   * Per-deploy secret-scan audit row (PR-A / ADR-101). Mirrors
   * the `scan` field shape — absent when the row has not been
   * scanned yet (deploy mid-pipeline or pre-PR-A), present
   * with `findings=[]` for a clean walk, present with
   * one-or-more entries for a hit. Read by the dashboard's
   * "secret scan" card and the CLI's `--show-secret-scan`
   * flag. Stamped both for the imaged-side layer walk (main
   * image + each sidecar; post-build, loud-fail on any
   * finding) and — forward-compat — for the apid-side
   * source-tree 422 path. Status closed-set the writer
   * stamps: "complete" (clean) | "complete_with_redactions"
   * (hit). The `image_digest` sub-field records which OCI
   * digest the imaged walk ran against; null on legacy
   * pre-PR-A rows. See `pkg/imaged/secretscan.go`.
   *
   */
  secret_scan?: (SecretScanResult | null);
  /**
   * Auto-detected build plan (issue #961 / Mega-A PR-2). One-line summary the CLI prints after `gregale deploy`. nil for image deploys.
   */
  build_plan?: (BuildPlan | null);
  /**
   * UUID of the deploying local account (FK → accounts.id, ON DELETE SET NULL). Empty when the deploy came from a non-local source (e.g. a githubd pusher not bound to a local account).
   */
  deployed_by_user_id?: string | null;
  /**
   * Closed-set classifier of how this deployment was submitted. One of `api` (SDK / API key) / `cli` (bearer token) / `dashboard` (session cookie) / `github` (githubd_bridge) / `operator` (admin). Enforced at the schema layer by migrations/00303_deployments_actor.sql's CHECK constraint.
   */
  deployed_via?: 'api' | 'cli' | 'dashboard' | 'github' | 'operator';
  /**
   * Trusted remote IP captured by `pkg/middleware.ClientIP` at handler entry (XFF + loopback trust contract). Loopback (127.0.0.1) for the githubd_bridge path. Both IPv4 and IPv6 are accepted at the wire and stored in Postgres' native `inet` type (which canonicalises both families); the OpenAPI schema intentionally omits `format: ipv4` so v6 deployments (which grow as the public gateway picks up AAAA records) do not fail schema validation. v6 is rendered as the bracketed colon-hex form per RFC 5952.
   */
  deployed_from_ip?: string | null;
  /**
   * Raw GitHub login of the pusher when `deployed_via == "github"`. Empty for all other via values. Distinct from the human-readable `DeployedBy` text column (issue #977 / PR #984) — pusher_login is the unmodified GH identity, suitable for downstream GitHub-API correlation.
   */
  pusher_login?: string | null;
  /**
   * Free-form operator note on the source-ref deploy request (≤280 chars). Example: 'Emergency rollback after payment provider incident'.
   */
  reason?: string;
  /**
   * Closed-set annotation tag on the source-ref deploy request for grouping/filtering.
   */
  tag?: 'incident_recovery' | 'hotfix' | 'scheduled_maintenance' | 'compliance_hold' | 'partner_request';
  /**
   * Human-readable actor label on the source-ref deploy request. CLI auto-captures from `git config user.name`; githubd stamps pusher.name; the GitHub Action defaults to ${{ github.actor }}.
   */
  deployed_by?: string;
  /**
   * Pull-request number that drove this source-ref deploy request (githubd pull_request.number; Action ${{ github.event.pull_request.number }}). NULL for push-to-main with no inferred PR.
   */
  pr_number?: number;
  /**
   * Per-deployment auto-rollback opt-in (issue #961 leaf 8 / ADR-118 / Mega-C PR-2). Customer sets this at create time (Pro+ only); schedd fires the rollback when first_5xx_count crosses the per-plan threshold inside first_5xx_window_ends_at.
   */
  rollback_on_5xx?: boolean;
  /**
   * Wall-clock timestamp of the first customer-visible wake response (anchor for the auto-rollback window). NULL until the gateway stamps it on the first wake.proxy_first_byte event.
   */
  first_wake_at?: string | null;
  /**
   * Wall-clock timestamp the auto-rollback window closes (first_wake_at + 5 min). NULL until the gateway stamps it on the first wake. The schedd scan checks `now() < first_5xx_window_ends_at` before firing the rollback.
   */
  first_5xx_window_ends_at?: string | null;
  /**
   * Atomic 5xx counter incremented by schedd on every wake.response_5xx event for this row. Default 0; NOT NULL DEFAULT 0 enforced at the schema layer.
   */
  first_5xx_count?: number;
  /**
   * Wall-clock timestamp the most recent auto-rollback fired (idempotent across retries; updated by schedd when the rollback tx commits). NULL until the first auto-rollback.
   */
  last_auto_rollback_at?: string | null;
  /**
   * Closed-set classifier for the most recent auto-rollback trigger. `threshold_exceeded` = first_5xx_count crossed the per-plan threshold inside the window. `first_window_expired` = the window expired without crossing the threshold (clean wake window). Closed-set is enforced at the schema layer via deployments_last_auto_rollback_reason_check.
   */
  last_auto_rollback_reason?: 'threshold_exceeded' | 'first_window_expired';
  /**
   * Canary preset used by the deployment's progressive rollout. `none` preserves the default 100% deployment path.
   */
  canary_preset?: 'none' | 'slow' | 'balanced' | 'aggressive' | '1-10-50-100';
  /**
   * Current zero-based canary ladder step.
   */
  canary_step?: number;
  /**
   * Total number of canary ladder steps; zero means no canary ladder.
   */
  canary_total_steps?: number;
  /**
   * Wall-clock timestamp at which the current canary step began.
   */
  canary_step_started_at?: string | null;
  /**
   * Durable rollout state used by the canary orchestrator and operator recovery path.
   */
  rollout_state?: 'pending' | 'rolling_out' | 'complete' | 'aborted';
  /**
   * Wall-clock timestamp at which rollout processing began.
   */
  rollout_started_at?: string | null;
  /**
   * Wall-clock timestamp at which the rollout reached complete.
   */
  rollout_completed_at?: string | null;
  /**
   * Wall-clock timestamp at which the rollout was aborted.
   */
  rollout_aborted_at?: string | null;
  /**
   * Operator or orchestrator reason recorded when the rollout is aborted.
   */
  rollout_aborted_reason?: string;
};

