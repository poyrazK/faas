/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact configured route and signal, optionally scoped to a recorded customer identity.
 */
export type RouteHealthInvestigationSelection = {
  method: string;
  path: string;
  status_code: 0 | 401 | 403 | 404 | 422 | 429;
  customer_group_by?: 'tenant' | 'consumer';
  customer_id?: string;
};

