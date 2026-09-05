/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsNodeRow } from './ObsNodeRow.js';
/**
 * Cursor-paginated node inventory for the operator console.
 */
export type ObsNodeListResponse = {
  items: Array<ObsNodeRow>;
  next_cursor: string;
  limit: number;
};

