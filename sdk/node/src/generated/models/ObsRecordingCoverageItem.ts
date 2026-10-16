/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRecordingAlertReferences } from './ObsRecordingAlertReferences.js';
import type { ObsRuleCoverageItem } from './ObsRuleCoverageItem.js';
/**
 * One expected recording definition and its bounded direct or transitive expected alert references; references do not prove runtime impact.
 */
export type ObsRecordingCoverageItem = {
  comparison: ObsRuleCoverageItem;
  /**
   * Known expected syntactic references; partial mapping can leave additional unresolved dependencies.
   */
  dependent_alerts: ObsRecordingAlertReferences;
};

