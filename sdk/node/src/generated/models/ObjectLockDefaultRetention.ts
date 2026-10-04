/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectRetentionPeriod } from './ObjectRetentionPeriod.js';
/**
 * A mode with exactly one fixed duration, an event hold duration or both. Null values and an empty default are rejected.
 */
export type ObjectLockDefaultRetention = {
  mode: 'GOVERNANCE' | 'COMPLIANCE';
  days?: number;
  years?: number;
  default_event_hold?: ObjectRetentionPeriod;
};

