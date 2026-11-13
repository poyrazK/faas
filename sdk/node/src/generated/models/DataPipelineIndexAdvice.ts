/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineIndexKey } from './DataPipelineIndexKey.js';
/**
 * Frozen metadata-based mirror index advice. Size is a conservative estimate, not measured query performance. Explicit approval permits a transactional build for tables up to 64 MiB, indexes up to 32 MiB, at most eight owned indexes per pipeline, with a two-second deadline and 250 ms lock timeout. Failures roll back and require a fresh preview.
 */
export type DataPipelineIndexAdvice = {
  id: string;
  state: string;
  preview_revision: number;
  table: string;
  keys: Array<DataPipelineIndexKey>;
  index_name?: string;
  statement?: string;
  table_bytes?: number;
  estimated_index_bytes?: number;
  storage_budget_bytes?: number;
  applied_bytes?: number;
  can_apply: boolean;
  reason?: string;
  message: string;
};

