/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Authenticated regional identity attestation for one non-deleted app. account_id retains app creator metadata; org_id identifies a shared organization. Not deployment readiness.
 */
export type CellAppBindingResponse = {
  cell_id: string;
  region: string;
  account_id: string;
  /**
   * Shared organization attested by this ownership proof; omitted for personal apps.
   */
  org_id?: string;
  app_id: string;
  app_slug: string;
};

