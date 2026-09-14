/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One chronological event timeline entry. posted_at is immutable; edited_at discloses a later text correction. Optional impact and components record a re-rating made with this update.
 */
export type PublicStatusUpdate = {
  id: string;
  state: 'investigating' | 'identified' | 'monitoring' | 'resolved' | 'scheduled' | 'in_progress' | 'completed' | 'cancelled';
  message: string;
  posted_at: string;
  edited_at?: string;
  impact?: 'maintenance' | 'degraded' | 'partial_outage' | 'major_outage';
  components?: Array<'api_console' | 'deployments' | 'app_execution' | 'networking' | 'observability'>;
};

