/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Application-reported effect. Text bounds are UTF-8 bytes; confirmed status requires a nonempty reference. Amount/currency are supplied together in minor units.
 */
export type OperationBusinessEffect = {
  workflow: string;
  instance_id: string;
  state: string;
  operation: string;
  code: string;
  version: string;
  description: string;
  status: 'pending' | 'failed' | 'confirmed';
  reference?: string;
  amount_minor?: number;
  currency?: string;
};

