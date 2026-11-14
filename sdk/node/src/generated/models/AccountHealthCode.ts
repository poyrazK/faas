/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * How often one problem code was returned in the window.
 */
export type AccountHealthCode = {
  /**
   * Problem code from the response body and X-Faas-Error-Code.
   */
  code: string;
  /**
   * Responses with this code.
   */
  count: number;
  /**
   * Most recent response with this code.
   */
  last_at: string;
};

