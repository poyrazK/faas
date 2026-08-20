/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-deployment preview URL read seam response.
 * Issue #976 / ADR-122 / SAFE-RELEASES-C.2.
 * Mirrors the api.DeploymentPreviewURL struct in pkg/api/dto.go.
 *
 */
export type DeploymentPreviewURL = {
  /**
   * Echoed from the path so a batch caller can correlate.
   */
  deployment_id: string;
  /**
   * Resolved parent app id.
   */
  app_id: string;
  /**
   * Per-deployment preview hostname (`deploy-{N}.{slug}.gregale.dev`). Empty when alive=false OR when wire.DeployWildcardSuffix is "" (zone disabled).
   */
  host?: string;
  /**
   * Full request URL (`https://<host>`). Empty when host is empty.
   */
  url?: string;
  /**
   * True iff the deployment exists, belongs to the caller, and has a status in {pending, building, imaging, snapshotting, live} (the same predicate as state.Deployment.DeploymentPreviewActive the cert allowlist consults).
   */
  alive: boolean;
  /**
   * When certmagic last validated the cert under host. Null for never-touched hostnames. NOT a latency probe — the cert NotAfter is the load-bearing expiry.
   */
  last_checked_at?: string | null;
};

