/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConfigGuardSummary } from './ConfigGuardSummary.js';
/**
 * The config revision an env or secret write recorded and the guard watching it.
 */
export type ConfigChangeReceipt = {
  revision: number;
  guard?: ConfigGuardSummary;
};

