/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectRetentionPeriod } from './ObjectRetentionPeriod.js';
/**
 * Verified native retention or a retention intent. An empty object requests a clear. Active fixed retention cannot be shortened and active COMPLIANCE cannot be downgraded. Enrolled event hold ON requires one duration; OFF omits duration and lets the provider fix the final date from the existing hold. Observed dates and requested minimum dates are preserved. Governance bypass is unsupported. Per-write protection remains fixed retention only.
 */
export type ObjectVersionRetention = {
  mode?: 'GOVERNANCE' | 'COMPLIANCE';
  retain_until_date?: string;
  event_hold?: 'ON' | 'OFF';
  event_hold_duration?: ObjectRetentionPeriod;
};

