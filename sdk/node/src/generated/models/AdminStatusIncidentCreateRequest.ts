/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Publish an active incident. Terminal and maintenance lifecycle states are not valid at creation.
 */
export type AdminStatusIncidentCreateRequest = {
  kind: 'incident';
  title: string;
  impact: 'degraded' | 'partial_outage' | 'major_outage';
  components: Array<'api_console' | 'deployments' | 'app_execution' | 'networking' | 'observability'>;
  state?: 'investigating' | 'identified' | 'monitoring';
  starts_at?: string;
  message: string;
};

