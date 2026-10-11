/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationBusinessEffectReference } from './OperationBusinessEffectReference.js';
/**
 * Application-reported compensation workflow observation. Confirmed status requires a nonempty reference. Text bounds are UTF-8 bytes without control characters. Source must be a retained confirmed effect in the same account, app, customer, and environment.
 */
export type OperationBusinessCompensation = {
  workflow: string;
  instance_id: string;
  state: string;
  operation: string;
  code: string;
  version: string;
  description: string;
  status: 'required' | 'pending' | 'failed' | 'confirmed';
  reference?: string;
  source_effect: OperationBusinessEffectReference;
};

