/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentConfigChange } from './ProjectEnvironmentConfigChange.js';
/**
 * One workload's live release identity in a source and target environment.
 */
export type ProjectEnvironmentPromotionChange = {
  /**
   * Production secret key names and change kinds, without values or fingerprints.
   */
  secret_changes?: Array<ProjectEnvironmentConfigChange>;
  /**
   * Identity of the approved immutable HTTP variable snapshot and environment versions; destination secrets and bindings remain owned by the target.
   */
  release_configuration_hash?: string;
  /**
   * Variable key names and change kinds only; values and secret material are omitted.
   */
  environment_changes?: Array<ProjectEnvironmentConfigChange>;
  source_workload_config_hash?: string;
  target_workload_config_hash?: string;
  promoted_workload_config_hash?: string;
  workload_slug: string;
  workload_name: string;
  kind: 'source_missing' | 'create' | 'update' | 'unchanged';
  source_deployment_id?: string;
  target_deployment_id?: string;
  source_build_id?: string;
  target_build_id?: string;
  source_revision?: string;
  target_revision?: string;
  source_revision_kind?: 'source_sha256' | 'image_digest' | 'commit_sha' | 'deployment_id';
  target_revision_kind?: 'source_sha256' | 'image_digest' | 'commit_sha' | 'deployment_id';
};

