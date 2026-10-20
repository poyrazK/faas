/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * What one daemon instance reported applying for a runtime-configuration key.
 */
export type OperatorRuntimeConfigApplyReceipt = {
  config_key: string;
  daemon: string;
  instance: string;
  version: number;
  status: 'applied' | 'failed';
  error?: string;
  reported_at: string;
};

