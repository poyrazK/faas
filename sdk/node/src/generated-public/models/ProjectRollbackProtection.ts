/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Saved retention window and storage size for a protected deploy group.
 */
export type ProjectRollbackProtection = {
  deploy_group_id: string;
  expires_at: string;
  /**
   * Bytes of unique root filesystem and sidecar layers held for this deploy.
   */
  layer_bytes: number;
};

