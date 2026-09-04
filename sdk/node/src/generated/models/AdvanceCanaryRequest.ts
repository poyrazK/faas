/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for POST /v1/deployments/{id}/canary/advance (issue #976 / ADR-122). expected_step is the persisted canary step observed by the progression worker; APID derives the next traffic percentage and rejects stale observations.
 */
export type AdvanceCanaryRequest = {
  /**
   * The canary_step the caller read before requesting the next stage.
   */
  expected_step: number;
};

