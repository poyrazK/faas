/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One would-ship-a-problem row. Stable RFC 7807 code
 * (matches [api.Code*] constants) so the CLI error renders
 * identically to what a real deploy would say.
 *
 */
export type DiffBreak = {
  /**
   * Stable RFC 7807 code; matches api.Code* constants
   */
  code: string;
  severity: 'error' | 'warn';
  reason: string;
  /**
   * Optional scope-wide ('memory') or per-row ('environment.<scope>.<key>').
   */
  field?: string;
  /**
   * JSON-encoded observed value (int / string / []string / …).
   */
  observed?: any;
  /**
   * JSON-encoded limit value.
   */
  limit?: any;
};

