/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeCommandCronClassName } from './ObsRuntimeCommandCronClassName.js';
import type { ObsRuntimeCommandCronClassValues } from './ObsRuntimeCommandCronClassValues.js';
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
/**
 * One ordered schedule class. Usable values require fresh successful collection after the evidence boundary and matching original scrape clocks. Valid partial positives survive sibling gaps.
 */
export type ObsRuntimeCommandCronClass = {
  class: ObsRuntimeCommandCronClassName;
  state: 'nonempty' | 'empty' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimeCommandCronClassValues;
};

