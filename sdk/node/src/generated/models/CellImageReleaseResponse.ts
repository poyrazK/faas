/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CellImageReleaseOrdering } from './CellImageReleaseOrdering.js';
/**
 * Durable regional acceptance receipt. This does not establish global readiness or routing eligibility.
 */
export type CellImageReleaseResponse = {
  ordering?: CellImageReleaseOrdering;
  cell_id: string;
  region: string;
  regional_account_id: string;
  regional_app_id: string;
  release_id: string;
  image: string;
  /**
   * Null after collection; the release cannot be executed again.
   */
  deployment_id: string | null;
  /**
   * Current regional deployment status, or expired after collection.
   */
  deployment_status: string;
  created_at: string;
  /**
   * Whether PUT returned an existing acceptance receipt. Always false for GET.
   */
  replayed: boolean;
};

