/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectRetentionPeriod } from './ObjectRetentionPeriod.js';
/**
 * Verified native retention, or a fixed-retention intent. An empty object requests a clear; active retention cannot be shortened without bypass, which is unsupported. Event hold fields are observation only for this contract.
 */
export type ObjectVersionRetention = {
  mode?: 'GOVERNANCE' | 'COMPLIANCE';
  retain_until_date?: string;
  event_hold?: 'ON' | 'OFF';
  event_hold_duration?: ObjectRetentionPeriod;
};

