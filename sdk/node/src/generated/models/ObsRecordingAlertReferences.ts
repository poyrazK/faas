/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRecordingAlertReference } from './ObsRecordingAlertReference.js';
/**
 * Complete count of this known catalog reference list before its five-item preview; unresolved dependencies can remain outside a dependent-alert list.
 */
export type ObsRecordingAlertReferences = {
  items: Array<ObsRecordingAlertReference>;
  total_items: number;
  limit: number;
  truncated: boolean;
};

