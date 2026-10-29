/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeletedAppSummary } from './DeletedAppSummary.js';
/**
 * Bounded account-owned tombstone inventory and its opaque cursor.
 */
export type DeletedAppListResponse = {
  account_id: string;
  apps: Array<DeletedAppSummary>;
  next_cursor?: string;
  evaluated_at: string;
};

