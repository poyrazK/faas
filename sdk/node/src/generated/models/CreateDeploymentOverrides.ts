/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentHealthcheck } from './DeploymentHealthcheck.js';
import type { DeploymentLivenessProbe } from './DeploymentLivenessProbe.js';
/**
 * Fargate-shaped deploy-time override object on `POST /v1/apps/{slug}/deployments`
 * (issue #460 / ADR-053). Field list is FROZEN — six fields, no more. Any extra
 * field on this object 400s the request. ADR-053 §Decision 1 documents the freeze;
 * the handler enforces it via `DisallowUnknownFields` on the JSON decoder.
 *
 * - `entrypoint` replaces the OCI image's ENTRYPOINT/CMD argv at exec time.
 * - `cmd` is appended to `entrypoint` (mirrors the OCI runtime contract).
 * - `env` is plaintext; env-var keys must match `^[A-Z][A-Z0-9_]*$`; per-value
 * byte cap = plan `EnvValueMaxBytes`.
 * - `env_secrets` carries `secret:NAME` REFS — the runtime resolver (PR-B)
 * strips the prefix and looks up `NAME` against the existing `app_secrets`
 * table. The ref name must match `^[A-Z][A-Z0-9_]*$`.
 * - `env` + `env_secrets` share the plan `EnvVarsMax` quota — no bypass by
 * mixing the two surfaces.
 * - `port` is per-deployment (1..65535; 0 = absent / fall back to image default).
 * - `healthcheck` is the readiness-probe shape; the actual HTTP probe ships
 * in a follow-up ADR.
 *
 */
export type CreateDeploymentOverrides = {
  /**
   * Replaces the OCI image's ENTRYPOINT/CMD argv. Each element must be non-empty.
   */
  entrypoint?: Array<string>;
  /**
   * Appended to entrypoint at exec time (OCI runtime contract). Each element must be non-empty.
   */
  cmd?: Array<string>;
  /**
   * Plaintext env map applied at boot. Keys: `^[A-Z][A-Z0-9_]*$`. Per-value byte cap = plan EnvValueMaxBytes.
   */
  env?: Record<string, string>;
  /**
   * Sealed-secret-ref env map. Each VALUE is `secret:NAME`; the runtime resolver looks up `NAME` against `app_secrets` at wake. Counts against the shared `EnvVarsMax` cap with `env`.
   */
  env_secrets?: Record<string, string>;
  /**
   * Listen port; 0 = absent / fall back to image default (today 8080).
   */
  port?: number;
  /**
   * Readiness-probe shape. Persisted today; the HTTP probe variant ships in a follow-up ADR.
   */
  healthcheck?: (DeploymentHealthcheck | null);
  /**
   * Liveness-probe override (issue #554 / ADR-078). The host (cmd/vmmd)
   * polls the guest's vsock 1028 STREAM on every `interval_s`; after
   * `consecutive_failures` consecutive non-2xx (or timeout / conn-refused)
   * responses, the VM is destroyed and schedd cold-boots it from rootfs
   * (per ADR-005 — never snapshot-restore). 3 restarts in 300 s
   * park the deployment.
   *
   * Per-plan gates (issue #554): Free is locked out (Plan.LivenessAllowed()
   * returns false; the apid handler rejects with
   * `plan_liveness_probe_not_allowed` BEFORE the DB is touched); Hobby,
   * Pro, Scale inherit the 5 s / 3 consecutive / 60 s cooldown / 3 in 300 s
   * defaults. Pro and Scale may select standard gRPC health.v1 Check
   * on the runtime port; Free and Hobby remain HTTP-only for liveness.
   *
   */
  liveness_probe?: (DeploymentLivenessProbe | null);
  /**
   * Override-object per-deployment env scope (ADR-091 / PR-D). Lowercase alnum + dash, 3..40 chars, no leading/trailing dash. nil/omitted = inherit top-level scope or `default`.
   */
  scope?: string | null;
};

