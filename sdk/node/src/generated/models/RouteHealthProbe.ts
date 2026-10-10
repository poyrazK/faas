/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in synthetic probe for a GET or HEAD selector (ADR-945). While a canary is in flight and organic evidence stays sparse, Gregale sends a few bodyless requests per minute to the candidate and stable deployments with customer auth gates unchanged. Probe requests never appear in request telemetry, analytics or usage; they wake the app like any request. At most 5 selectors per app.
 */
export type RouteHealthProbe = {
  /**
   * Concrete absolute path matching the selector, with a value for each {parameter}, for example /users/42 for /users/{id}. No query, fragment or wildcard.
   */
  path: string;
};

