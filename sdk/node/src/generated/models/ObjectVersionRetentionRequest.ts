/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectVersionRetention } from './ObjectVersionRetention.js';
/**
 * Stable identity and fixed or enrolled event hold retention intent for an owned version. ON requires a duration; OFF omits duration and requires a previously active hold unless a fixed date is supplied.
 */
export type ObjectVersionRetentionRequest = {
  /**
   * Stable caller-generated UUID v4; reuse only for the identical intent.
   */
  id: string;
  retention: ObjectVersionRetention;
};

