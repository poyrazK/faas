/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Schedule maintenance with a bounded start/end window.
 */
export type AdminStatusMaintenanceCreateRequest = {
  kind: 'maintenance';
  title: string;
  impact?: 'maintenance';
  components: Array<'api_console' | 'deployments' | 'app_execution' | 'networking' | 'observability'>;
  state?: 'scheduled';
  scheduled_start_at: string;
  scheduled_end_at: string;
  message: string;
};

