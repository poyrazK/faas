/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectReleaseAddition } from './ProjectReleaseAddition.js';
import type { ProjectReleaseCommand } from './ProjectReleaseCommand.js';
import type { ProjectReleaseConfiguration } from './ProjectReleaseConfiguration.js';
import type { ProjectReleaseRemoval } from './ProjectReleaseRemoval.js';
import type { ProjectReleaseVerification } from './ProjectReleaseVerification.js';
import type { ProjectReleaseWorker } from './ProjectReleaseWorker.js';
/**
 * Server-owned safe project release checkpoint. Activation occurs only after all zero-traffic candidates complete readiness.
 */
export type ProjectSafeRelease = {
  /**
   * Explicit opt-in to reserve new HTTP source services.
   */
  add_services?: boolean;
  additions?: Array<ProjectReleaseAddition>;
  /**
   * Explicit opt-in to retire serving HTTP services the source no longer declares.
   */
  remove_services?: boolean;
  removals?: Array<ProjectReleaseRemoval>;
  status: 'building' | 'activated' | 'failed' | 'conflicted' | 'verifying' | 'succeeded' | 'rolling_back' | 'rolled_back' | 'rollback_failed';
  previous_release_id: string;
  release_id?: string;
  ttl_seconds: number;
  deadline: string;
  message?: string;
  verification?: ProjectReleaseVerification;
  configuration?: ProjectReleaseConfiguration;
  release_command?: ProjectReleaseCommand;
  worker?: ProjectReleaseWorker;
  restored_release_id?: string;
};

