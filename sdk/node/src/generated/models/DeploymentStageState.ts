/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentBuildDependencyGate } from './DeploymentBuildDependencyGate.js';
import type { DeploymentDependencyGate } from './DeploymentDependencyGate.js';
import type { DeploymentHostingVerification } from './DeploymentHostingVerification.js';
import type { DeploymentStageHistoryEntry } from './DeploymentStageHistoryEntry.js';
/**
 * Customer-visible deployment stage progress. `current` is the open stage, or an empty string once the deployment is live, failed or cancelled and that stage has moved into `history`.
 */
export type DeploymentStageState = {
  current: '' | 'source_download' | 'dependency_restore' | 'image_build' | 'security_scan' | 'snapshot_prepare' | 'readiness';
  /**
   * Start of the open stage. Null or absent when no stage is open, and on legacy rows created before stage timing.
   */
  current_started_at?: string | null;
  /**
   * Closed stages, oldest first. Writers keep the most recent 64 (MaxStageHistory).
   */
  history: Array<DeploymentStageHistoryEntry>;
  /**
   * Stage a retry asked to resume from. Present only on retry deployments, which always restart at source_download.
   */
  retry_requested_stage?: 'source_download' | 'dependency_restore' | 'image_build' | 'security_scan' | 'snapshot_prepare' | 'readiness';
  /**
   * Why a retry restarted from source_download rather than retry_requested_stage.
   */
  retry_restart_reason?: string;
  dependency_gate?: DeploymentDependencyGate;
  build_dependency_gate?: DeploymentBuildDependencyGate;
  hosting_verification?: DeploymentHostingVerification;
};

