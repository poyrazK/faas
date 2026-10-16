/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One graph-owned production push worker, its immutable binding intent and runtime admission evidence.
 */
export type ProjectReleaseWorker = {
  app_id: string;
  slug: string;
  binding_id: string;
  binding_hash: string;
  previous_deployment_id: string;
  drain_seconds: number;
  status: 'building' | 'ready' | 'active' | 'restored' | 'failed' | 'conflicted';
  deployment_id?: string;
  instance_id?: string;
};

