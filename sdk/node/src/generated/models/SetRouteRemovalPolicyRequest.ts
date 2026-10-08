/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type SetRouteRemovalPolicyRequest = {
  expected_revision: number;
  mode: 'report' | 'enforce';
  /**
   * Go duration between 1h and 2160h; default 720h.
   */
  grace_period?: string;
  /**
   * Go duration between 1m and 72h; default 1h.
   */
  max_approval_age?: string;
};

