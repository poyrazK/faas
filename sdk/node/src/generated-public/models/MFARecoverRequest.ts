/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for /recover — one of the 10 recovery codes the
 * customer received on /enroll. The hash is removed from
 * the stored set; subsequent /recover calls with the same
 * code return 401.
 *
 */
export type MFARecoverRequest = {
  code: string;
  /**
   * Dashboard CSRF token for the recover mutation.
   */
  csrf_token?: string;
};

