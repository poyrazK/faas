/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Terminal deployment outcome for a surface explicitly linked to this tenant. Contains revision and status metadata only; app/deployment IDs, source details, logs, and raw errors are excluded. A failed latest attempt does not mean a previous deployment is not serving.
 */
export type PlatformTenantSurfaceDeploymentChangedWebhookPayload = {
  platform_tenant_id: string;
  external_ref: string;
  surface_id: string;
  surface_name: string;
  revision: number;
  deployment_status: 'live' | 'failed';
  started_at: string;
  changed_at: string;
};

