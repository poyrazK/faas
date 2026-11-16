/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Monotonic checkpoint from the authenticated local execution claim holder. Deployment and worker identities require independent fresh native observation during recovery; this metadata grants no action authority.
 */
export type TaskToolReleaseReceipt = {
  revision: number;
  release_id: string;
  stage: 'prepared' | 'deploying_worker' | 'deploying_web' | 'replacements_started' | 'web_promoted' | 'draining_previous' | 'previous_parked' | 'complete';
  serving_captured: boolean;
  serving_deployment_id?: string;
  web_deployment_id?: string;
  worker_deployment_id?: string;
  pending_submission?: 'web' | 'worker';
  previous_deployments: Record<string, string>;
  worker_ids: Array<string>;
  promoted: boolean;
  parked: Record<string, boolean>;
};

