/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppChangeEvent } from './AppChangeEvent.js';
import type { Incident } from './Incident.js';
import type { IncidentFire } from './IncidentFire.js';
import type { IncidentNote } from './IncidentNote.js';
import type { IncidentUpdate } from './IncidentUpdate.js';
export type IncidentDetail = {
  incident: Incident;
  fires?: Array<IncidentFire>;
  /**
   * What the logs showed at the newest fire, when log search was on.
   */
  log_context?: Record<string, any>;
  updates?: Array<IncidentUpdate>;
  changes?: Array<AppChangeEvent>;
  changes_truncated?: boolean;
  /**
   * The account's acknowledgement and notes, oldest first.
   */
  notes: Array<IncidentNote>;
  unavailable_sources: Array<string>;
};

