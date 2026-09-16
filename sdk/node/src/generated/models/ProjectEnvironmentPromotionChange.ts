/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One workload's live release identity in a source and target environment.
 */
export type ProjectEnvironmentPromotionChange = {
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

