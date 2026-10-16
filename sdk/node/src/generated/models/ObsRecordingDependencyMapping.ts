/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRecordingAlertReferences } from './ObsRecordingAlertReferences.js';
/**
 * Expected catalog reference completeness; partial means some selected alerts have unresolved syntactic recording dependencies.
 */
export type ObsRecordingDependencyMapping = {
  scope: string;
  state: 'complete' | 'partial';
  alert_set_revision: string;
  total_alerts: number;
  uncertain_alerts: ObsRecordingAlertReferences;
};

