/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresUsageImportWindow } from './ManagedPostgresUsageImportWindow.js';
export type ManagedPostgresUsageImportRequest = {
  /**
   * Durable request identity; preserve across retries.
   */
  import_id: string;
  /**
   * Independently accounted database; shared restores must import against their root.
   */
  database_id: string;
  /**
   * Non-secret retained evidence locator; never a credential or signed URL.
   */
  evidence_reference: string;
  /**
   * SHA-256 of the retained source artifact, attested by the operator; no artifact is fetched by this API.
   */
  evidence_sha256: string;
  reason: string;
  /**
   * Ascending contiguous complete policy-sized windows with exactly the backend's advertised meters. Costs are calculated from the current server policy. Maximum request size 1 MiB.
   */
  windows: Array<ManagedPostgresUsageImportWindow>;
  /**
   * Omit for preview; required for apply.
   */
  expected_revision?: string;
};

