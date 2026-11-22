/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AlertAnomalyDeploymentPromotion } from './AlertAnomalyDeploymentPromotion.js';
/**
 * Recorded full-traffic promotions for the selected environment. An app-wide replay includes default-environment promotions.
 */
export type AlertAnomalyDeploymentContext = {
  /**
   * Deployment environment scope.
   */
  scope: string;
  /**
   * Recorded promotions, newest first. Includes up to 100 in the selected two hours and the latest one before the window when available.
   */
  promotions: Array<AlertAnomalyDeploymentPromotion>;
  /**
   * True when more than 100 recorded promotions fall within the replay window.
   */
  truncated?: boolean;
};

