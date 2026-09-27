/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable graph identities involved in a graph-aware promotion and rollback.
 */
export type ProjectEnvironmentPromotionReleaseGraphResponse = {
  source_release_set_id?: string;
  previous_target_release_set_id?: string;
  target_release_set_id?: string;
  restored_target_release_set_id?: string;
  ttl_seconds: number;
};

