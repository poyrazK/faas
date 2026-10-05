/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectVersionRetention } from './ObjectVersionRetention.js';
/**
 * Stable identity and fixed retention intent for an owned version.
 */
export type ObjectVersionRetentionRequest = {
  /**
   * Stable caller-generated UUID v4; reuse only for the identical intent.
   */
  id: string;
  retention: ObjectVersionRetention;
};

