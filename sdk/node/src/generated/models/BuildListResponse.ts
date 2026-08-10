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
 * next_before cursor is the started_at of the OLDEST
 * non-null-started row on this page, formatted as RFC3339Nano
 * (mirror of DeploymentListResponse). An empty next_before
 * signals end-of-list.
 *
 * Mirrors DeploymentListResponse. The new index and the
 * sibling ListBuildsForAccountPaged method (state layer)
 * make this O(page-size) regardless of account size; the
 * unlimited ListBuildsForAccount(ctx, accountID) stays
 * intact for the GDPR export at cmd/apid/handlers_account.go.
 *
 */
export type BuildListResponse = {
  items: Array<BuildResponse>;
  /**
   * Cursor for the next page (RFC3339Nano). Empty string signals end-of-list.
   */
  next_before?: string;
};

