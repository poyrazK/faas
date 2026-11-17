/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskClassName } from './ObsRuntimeAppTaskClassName.js';
import type { ObsRuntimeAppTaskClassValues } from './ObsRuntimeAppTaskClassValues.js';
import type { ObsRuntimeAppTaskKindName } from './ObsRuntimeAppTaskKindName.js';
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
/**
 * One ordered kind/class observation. Values require successful fresh collection after the evidence boundary and coherent original sample clocks. Valid partial positives survive sibling gaps.
 */
export type ObsRuntimeAppTaskClass = {
  kind: ObsRuntimeAppTaskKindName;
  class: ObsRuntimeAppTaskClassName;
  state: 'nonempty' | 'empty' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimeAppTaskClassValues;
};

