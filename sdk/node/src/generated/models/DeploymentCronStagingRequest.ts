/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentCronConfig } from './DeploymentCronConfig.js';
/**
 * Immutable HTTP cron intent. Prepare retains active schedules until the submitted production release owns all traffic; cancel fences an unsubmitted intent. Stage and restore support legacy recovery.
 */
export type DeploymentCronStagingRequest = {
  /**
   * Owned opaque upload identity to track for autonomous committed-upload recovery.
   */
  upload_id?: string;
  session_id: string;
  operation: 'stage' | 'restore' | 'prepare' | 'cancel' | 'track_upload';
  expected_app_id: string;
  expected_deployment_id: string;
  expected_revision: number;
  before: Array<DeploymentCronConfig>;
  desired: Array<DeploymentCronConfig>;
  /**
   * Stages the manifest's HTTP crons into this non-production environment of the app's project; it activates when the environment's deployment owns all of its traffic and replaces only its manifest crons. Only prepare, cancel and track_upload take one.
   */
  environment?: string;
};

