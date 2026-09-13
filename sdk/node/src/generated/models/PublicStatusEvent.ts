/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PublicStatusUpdate } from './PublicStatusUpdate.js';
/**
 * Public incident or maintenance metadata and chronological timeline.
 */
export type PublicStatusEvent = {
  id: string;
  kind: 'incident' | 'maintenance';
  title: string;
  impact: 'maintenance' | 'degraded' | 'partial_outage' | 'major_outage';
  components: Array<'api_console' | 'deployments' | 'app_execution' | 'networking' | 'observability'>;
  state: 'investigating' | 'identified' | 'monitoring' | 'resolved' | 'scheduled' | 'in_progress' | 'completed' | 'cancelled';
  starts_at?: string;
  scheduled_start_at?: string;
  scheduled_end_at?: string;
  updated_at: string;
  resolved_at?: string;
  updates: Array<PublicStatusUpdate>;
};

