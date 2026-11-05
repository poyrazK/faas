/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PublicStatusComponent } from './PublicStatusComponent.js';
import type { PublicStatusEvent } from './PublicStatusEvent.js';
import type { PublicStatusIndicator } from './PublicStatusIndicator.js';
/**
 * Complete public status snapshot for the single Gregale region.
 */
export type PublicStatusOverview = {
  overall_status: 'operational' | 'maintenance' | 'degraded' | 'partial_outage' | 'major_outage' | 'unknown';
  data_status: 'fresh' | 'stale' | 'unavailable';
  updated_at: string;
  region_scope: 'single-region';
  components: Array<PublicStatusComponent>;
  indicators: Array<PublicStatusIndicator>;
  active_events: Array<PublicStatusEvent>;
  upcoming_maintenance: Array<PublicStatusEvent>;
  resolved_incidents: Array<PublicStatusEvent>;
};

