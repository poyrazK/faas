/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppScalingSnapshot } from './AppScalingSnapshot.js';
/**
 * Original atomic scaling configuration commit; does not establish runtime readiness.
 */
export type AppScalingReceipt = (AppScalingSnapshot & {
  request_id: string;
  previous_revision: number;
  committed_at: string;
});

