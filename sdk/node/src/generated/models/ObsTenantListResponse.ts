/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsTenantRow } from './ObsTenantRow.js';
/**
 * Cursor-paginated tenant inventory for the operator console.
 */
export type ObsTenantListResponse = {
  items: Array<ObsTenantRow>;
  next_cursor: string;
  limit: number;
};

