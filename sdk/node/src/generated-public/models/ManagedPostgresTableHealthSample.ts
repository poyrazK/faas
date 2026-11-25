/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresTableHealthRow } from './ManagedPostgresTableHealthRow.js';
/**
 * Ranked table and leaf-partition measurements observed on the primary.
 */
export type ManagedPostgresTableHealthSample = {
  sampled_at: string;
  has_more: boolean;
  tables: Array<ManagedPostgresTableHealthRow>;
};

