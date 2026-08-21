/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CreateDeploymentOverrides } from './CreateDeploymentOverrides.js';
import type { Sidecar } from './Sidecar.js';
/**
 * Two content-types accepted (see operation description): prebuilt OCI image reference, or multipart source upload. The optional `overrides` object (issue #460 / ADR-053) lets a customer redeploy the same digest-pinned image with a different entrypoint / cmd / env / env_secrets / port / healthcheck without rebuilding the image. The override field list is FROZEN — six fields, no more — and any extra field on the override object 400s the request (the handler's decoder rejects unknown keys; see ADR-053 §Decision 1). The optional `sidecars` array (issue #463 / ADR-068) attaches up to 2 stateless sidecars (1 init + 1 sidecar) per app — a one-shot DB migrator as `init`, a metrics scraper as `sidecar`. nil/omitted = no sidecars.
 */
export type CreateDeploymentRequest = {
  /**
   * registry.gregale.dev/...@sha256:... — digest-pinned OCI reference.
   */
  image?: string;
  /**
   * Deploy-time overrides (entrypoint, cmd, env, env_secrets, port, healthcheck). nil/omitted = deploy the image as-is.
   */
  overrides?: (CreateDeploymentOverrides | null);
  /**
   * Per-deploy signature-enforcement opt-in (issue #472 / ADR-054). nil = inherit apps.require_signed; *true is a no-op when the app flag is already on; *false is rejected with 403 deploy_signature_invalid when the app flag is on (operator policy wins).
   */
  require_signed?: boolean | null;
  /**
   * Up to 2 stateless sidecars (1 init + 1 sidecar). nil/omitted = no sidecars. See ADR-068 for the hard 2-cap and stateless-only contract.
   */
  sidecars?: Array<Sidecar>;
  /**
   * Per-deployment traffic-split weight (issue #556 PR-A). nil = server default 100; explicit 0..100 = opt into canary (Pro/Scale only).
   */
  traffic_percent?: number | null;
  /**
   * Top-level per-deployment env scope (ADR-091 / PR-D). Lowercase alnum + dash, 3..40 chars, no leading/trailing dash. nil/omitted = `default`.
   */
  scope?: string | null;
};

