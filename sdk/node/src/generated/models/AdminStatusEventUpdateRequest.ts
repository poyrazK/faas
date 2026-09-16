/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Operator request to append a lifecycle update and optionally re-rate its impact or affected components.
 */
export type AdminStatusEventUpdateRequest = {
  state: 'investigating' | 'identified' | 'monitoring' | 'resolved' | 'scheduled' | 'in_progress' | 'completed' | 'cancelled';
  message: string;
  impact?: 'maintenance' | 'degraded' | 'partial_outage' | 'major_outage';
  components?: Array<'api_console' | 'deployments' | 'app_execution' | 'networking' | 'observability'>;
};

