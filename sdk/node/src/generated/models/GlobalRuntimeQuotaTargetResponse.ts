/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Original regional identity, reserved capacity and monotone issuance/release accounting.
 */
export type GlobalRuntimeQuotaTargetResponse = {
  cell_id: string;
  region: string;
  cell_generation: number;
  regional_account_id: string;
  /**
   * Snapshotted organization tenant; omitted for personal apps.
   */
  regional_org_id?: string;
  regional_app_id: string;
  regional_app_slug: string;
  concurrency: number;
  rollout_concurrency: number;
  /**
   * Durable directory issuance marker; does not prove delivery or scheduler acceptance.
   */
  issued_at: string | null;
  /**
   * Unissued retirement or internally verified regional drain release.
   */
  released_at: string | null;
};

