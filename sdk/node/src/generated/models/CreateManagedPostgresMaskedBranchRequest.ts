/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresMaskedTable } from './ManagedPostgresMaskedTable.js';
/**
 * Source table policies and configuration for creating a masked database copy.
 */
export type CreateManagedPostgresMaskedBranchRequest = {
  name: string;
  ttl_seconds?: number;
  tables: Array<ManagedPostgresMaskedTable>;
};

