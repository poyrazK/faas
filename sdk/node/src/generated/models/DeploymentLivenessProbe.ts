/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentGRPCLivenessProbe } from './DeploymentGRPCLivenessProbe.js';
/**
 * Liveness-probe shape on the deploy-time override object (issue #554 / ADR-078).
 * The probe is the Cloud-Run-parity primitive that asks "is the VM still
 * responding?" — a wedged guest (busy-loop, leaked fd, deadlocked runner)
 * sits resident billing RAM-hours while serving 5xx until the §13 idle
 * reaper eventually fires. The liveness probe replaces that wait with the
 * short, customer-visible path: 3 consecutive failures → destroy →
 * cold-boot from rootfs (per ADR-005 — never snapshot-restore).
 *
 * Distinct from `DeploymentHealthcheck`: readiness is "accept traffic?",
 * liveness is "still alive?". A passing readiness but failing liveness
 * is the canonical "wake succeeded, then the runtime died" failure mode
 * that this primitive is designed to catch.
 *
 * Validation rules (enforced in `pkg/api/dto.go::CreateDeploymentOverrides.Validate`):
 * - Exactly one of `path` (HTTP) or `grpc` (standard gRPC health Check) must be set.
 * The server enforces this mutual-exclusion rule.
 * - `path`, when set, must start with `/`.
 * - `grpc.service` is optional and limited to 256 characters.
 * - `interval_s` ∈ [0, 60] (0 = inherit per-plan default; Hobby/Pro/Scale → 5 s).
 * - `timeout_s` ∈ [0, 5] (0 = inherit 2 s) for either probe action.
 * - `consecutive_failures` ∈ [0, 10] (0 = inherit per-plan default; Hobby/Pro/Scale → 3).
 * - `cooldown_s` ∈ [10, 600] (cooldown gate enforced by the vmmd-side
 * probe loop after a destroy fires — see ADR-078; 0 = no cooldown, the
 * legacy Free-plan behaviour).
 *
 * Per-deployment overrides of `interval_s` / `timeout_s` / `consecutive_failures` / `cooldown_s`
 * are clamped to the bounds above. The counter resets to 0 on the first 2xx
 * and survives an intermittent 5xx across the consecutive window (AC #2:
 * flaky app does NOT oscillate). The cooldown gate short-circuits the
 * counter when a probe fires inside the previous destroy's window so the
 * cold-boot replacement instance has a grace period to settle.
 *
 */
export type DeploymentLivenessProbe = {
  /**
   * HTTP path the probe requests from the guest; must start with `/` (e.g. `/healthz`). Reuses the runner's existing listener — no runner changes (issue #554 §4). Set exactly one of path or grpc.
   */
  path?: string;
  /**
   * Use the standard gRPC health.v1 Check RPC. Set exactly one of path or grpc; Pro and Scale plans only.
   */
  grpc?: DeploymentGRPCLivenessProbe;
  /**
   * Per-plan poll cadence in seconds; 0 = inherit per-plan default (Hobby/Pro/Scale → 5 s). Clamped to [MinLivenessPeriodSeconds=1, MaxLivenessPeriodSeconds=60].
   */
  interval_s?: number;
  /**
   * Per-probe HTTP or gRPC timeout in seconds; 0 = inherit 2 s default. A timeout is treated identically to a non-2xx response by the failure counter.
   */
  timeout_s?: number;
  /**
   * N at which DestroyForLivenessFailure fires; 0 = inherit per-plan default (Hobby/Pro/Scale → 3). The counter is reset to 0 on the first 2xx and survives an intermittent 5xx across the consecutive window (AC #2 — flaky app does NOT oscillate).
   */
  consecutive_failures?: number;
  /**
   * Cooldown window in seconds after a liveness-driven destroy fires. While inside the window the vmmd-side probe loop short-circuits the failure counter so the cold-boot replacement has time to settle (issue #554 closure / ADR-078). 0 = no cooldown (Free-plan / legacy behaviour); clamped to [MinLivenessCooldownSeconds=10, MaxLivenessCooldownSeconds=600] when the field is populated by a Pro/Scale deployment override.
   */
  cooldown_s?: number;
};

