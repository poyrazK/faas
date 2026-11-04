/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentCronActivationStatus } from './DeploymentCronActivationStatus.js';
/**
 * Server-managed cron activation diagnostics. CLI-owned settings are additionally reflected in CLI receipt configuration_complete. Reads never create builds or modify schedules.
 */
export type DeploymentConfigurationStatus = {
  /**
   * True when no deferred cron activation is recorded or the tracked intent was activated.
   */
  complete: boolean;
  crons: DeploymentCronActivationStatus;
};

