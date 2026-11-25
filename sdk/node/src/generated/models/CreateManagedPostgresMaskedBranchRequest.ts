/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresMaskedTable } from './ManagedPostgresMaskedTable.js';
export type CreateManagedPostgresMaskedBranchRequest = {
  name: string;
  ttl_seconds?: number;
  tables: Array<ManagedPostgresMaskedTable>;
};

