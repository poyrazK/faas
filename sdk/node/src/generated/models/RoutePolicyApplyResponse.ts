/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RoutePolicyReceipt } from './RoutePolicyReceipt.js';
/**
 * Successful committed receipt with retry replay information and separate gateway convergence metadata.
 */
export type RoutePolicyApplyResponse = {
  receipt: RoutePolicyReceipt;
  replayed: boolean;
  gateway_state: 'active' | 'converging' | 'unknown' | 'unobserved';
  gateway_generation?: number;
};

