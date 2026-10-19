/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-time freshness assessment and safe recovery guidance. Unconfirmed checkpoints can occur on idle sources.
 */
export type DataPipelineHealth = {
  state: 'healthy' | 'attention' | 'unknown' | 'pending' | 'inactive';
  code: string;
  message: string;
  recovery: string;
  action?: 'resume' | 'rotate_credentials' | 'resync';
  requires_confirmation: boolean;
};

