/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One daemon/node acknowledgement of a runtime configuration version.
 */
export type OperatorRuntimeConfigAck = {
  consumer: string;
  node_id?: string;
  version: number;
  status: 'applied' | 'failed';
  effective_value?: any;
  error?: string;
  updated_at: string;
  applied_at?: string;
};

