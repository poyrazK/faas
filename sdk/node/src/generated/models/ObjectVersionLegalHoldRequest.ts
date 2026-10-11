/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectVersionLegalHold } from './ObjectVersionLegalHold.js';
/**
 * Stable identity and desired independent legal hold for an owned version.
 */
export type ObjectVersionLegalHoldRequest = {
  /**
   * Caller-generated UUID v4 for this legal-hold intent; retain it when retrying.
   */
  id: string;
  legal_hold: ObjectVersionLegalHold;
};

