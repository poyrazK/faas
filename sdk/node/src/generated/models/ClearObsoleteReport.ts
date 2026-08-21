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
   * Go duration (e.g. 168h).
   */
  older_than: string;
};

