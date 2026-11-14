/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineIndexOrder } from './DataPipelineIndexOrder.js';
/**
 * Structured mirror query shape with no SQL or filter values. preview queues metadata inspection; approve accepts only a frozen advice_id; discard clears unapproved advice. At most four keys including the tenant policy expression.
 */
export type DataPipelineIndexRequest = {
  expected_revision: number;
  action: 'preview' | 'approve' | 'discard';
  advice_id?: string;
  table?: string;
  filters?: Array<string>;
  order_by?: Array<DataPipelineIndexOrder>;
  max_index_bytes?: number;
};

