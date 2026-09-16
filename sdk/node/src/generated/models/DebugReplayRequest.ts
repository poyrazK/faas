/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Optional target selection for a metadata-only debugger replay. Empty body preserves the default mirror rule selected for the retained request's serving deployment.
 */
export type DebugReplayRequest = {
  /**
   * Enabled mirror target deployment for the retained request's serving deployment.
   */
  mirror_deployment_id?: string | null;
};

