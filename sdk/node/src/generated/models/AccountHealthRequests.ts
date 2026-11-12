/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountHealthCode } from './AccountHealthCode.js';
import type { AccountHealthRequest } from './AccountHealthRequest.js';
/**
 * API requests Gregale refused for the account in the recent window.
 */
export type AccountHealthRequests = {
  /**
   * Length of the window in hours.
   */
  window_hours: number;
  /**
   * Refused requests in the window.
   */
  total: number;
  /**
   * Counts by problem code, most frequent first, at most 10.
   */
  by_code: Array<AccountHealthCode>;
  /**
   * Most recent refused requests, at most 20.
   */
  recent: Array<AccountHealthRequest>;
};

