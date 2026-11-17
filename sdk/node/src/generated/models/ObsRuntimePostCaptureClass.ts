/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHandoffEvidenceReason } from './ObsRuntimeHandoffEvidenceReason.js';
import type { ObsRuntimePostCaptureClassName } from './ObsRuntimePostCaptureClassName.js';
import type { ObsRuntimePostCaptureClassValues } from './ObsRuntimePostCaptureClassValues.js';
/**
 * One ordered post-capture evidence class. Values require fresh successful collection after the evidence boundary, fresh scrape evidence and original class clocks matching collection metadata. Valid positive evidence survives gaps in other classes.
 */
export type ObsRuntimePostCaptureClass = {
  class: ObsRuntimePostCaptureClassName;
  state: 'nonempty' | 'empty' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeHandoffEvidenceReason;
  observed_at?: string;
  values?: ObsRuntimePostCaptureClassValues;
};

