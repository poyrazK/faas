/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppChangeEvent } from './AppChangeEvent.js';
/**
 * ADR-741 change timeline for one app over [since, until), newest first.
 */
export type AppChangeTimelineResponse = {
  app_id: string;
  app_slug: string;
  since: string;
  until: string;
  events: Array<AppChangeEvent>;
  /**
   * Older events in the window were dropped at the 200-event cap.
   */
  truncated: boolean;
  /**
   * Sources that could not be read; their events are missing, not absent.
   */
  unavailable_sources: Array<'deployment' | 'edge_rule' | 'runtime_config' | 'incident' | 'health' | 'activity'>;
};

