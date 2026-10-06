/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExclusiveOperationPolicy } from './ExclusiveOperationPolicy.js';
/**
 * Persisted policy revision and lifecycle metadata.
 */
export type ExclusiveWorkPolicyRecord = {
  id: string;
  revision: number;
  policy: ExclusiveOperationPolicy;
  retired: boolean;
  created_at: string;
  updated_at: string;
};

