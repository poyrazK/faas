/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Initial bounded route inventory from exact audit events.
 */
export type DiscoveredAuditRoutesResponse = {
  app_id: string;
  routes: Array<string>;
  cap_hit: boolean;
  source: 'request_audit';
};

