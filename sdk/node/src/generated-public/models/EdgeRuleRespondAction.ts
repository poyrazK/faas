/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Preview-only fixed JSON response. The gateway serves this body
 * directly for the matched route, so the preview frontend can be
 * developed before the backend endpoint exists. Status codes are
 * limited to 200..599 and the JSON body is capped at 64 KiB.
 *
 */
export type EdgeRuleRespondAction = {
  status_code: number;
  /**
   * Arbitrary JSON response body. Omit for an empty response.
   */
  body?: any;
};

