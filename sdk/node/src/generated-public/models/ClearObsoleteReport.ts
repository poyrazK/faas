/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Response for POST /v1/apps/{slug}/deployments/clear-obsolete (ADR-124). Count is the number of soft-deleted rows in this call; OlderThan echoes the cutoff the store applied (default 168h).
 */
export type ClearObsoleteReport = {
  app_slug: string;
  count: number;
  /**
   * Echoes the cutoff the store applied to this clear pass (e.g. 168h = 7 days).
   */
  older_than: string;
};

