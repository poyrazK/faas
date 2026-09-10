/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One non-secret release field that changed from the previous deployment.
 */
export type DeploymentChange = {
  field: 'status' | 'kind' | 'build_id' | 'image_digest' | 'source_url' | 'commit_sha' | 'source_root' | 'scope' | 'build_plan' | 'min_instances' | 'traffic_percent' | 'has_overrides' | 'canary_preset' | 'rollback_on_5xx' | 'rollout_state';
  before: string | number | boolean | any | null;
  after: string | number | boolean | any | null;
};

