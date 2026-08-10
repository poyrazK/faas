/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BuildResponse } from './BuildResponse.js';
/**
 * DEPLOY-PROV-6 follow-up / ADR-091 (issue #741 close-out):
 * the page shape for GET /v1/builds. Items is one page of
 * builds ordered started_at DESC NULLS LAST — queued builds
 * (started_at IS NULL) sink to the bottom of the first page
 * per the new index builds_deployment_started_idx. The
 * next_before cursor is the opaque tuple
 * `<rfc3339nano>|<id_hex>` of the LAST row on this page
 * (post-review fix; code review surfaced issues 1+2 that
 * the id tiebreaker resolves). The started_at segment is
 * empty for queued-tail pages (the cursor becomes a pure
 * id anchor in the NULL zone). An empty next_before
 * signals end-of-list. Round-trip the cursor verbatim on
 * subsequent requests; do NOT re-parse or re-encode.
 *
 * Mirrors DeploymentListResponse in shape only. The new
 * index and the sibling ListBuildsForAccountPaged method
 * (state layer) make this O(page-size) regardless of
 * account size; the unlimited
 * ListBuildsForAccount(ctx, accountID) stays intact for the
 * GDPR export at cmd/apid/handlers_account.go.
 *
 */
export type BuildListResponse = {
  items: Array<BuildResponse>;
  /**
   * Opaque cursor for the next page. Empty = end of list.
   * Wire format: `<rfc3339nano>|<id_hex>` (pipe-separated).
   * The id is the Build.ID of the LAST row on this page;
   * the started_at segment is RFC3339Nano (sub-second
   * preserved) for non-queued rows, or empty for queued
   * rows (the cursor then keys on id alone in the NULL
   * zone). Round-trip verbatim — server-parsed, no client
   * re-encoding.
   *
   */
  next_before?: string;
};

