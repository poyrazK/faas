/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AlertObservedIncidentOpeningEvidence } from './AlertObservedIncidentOpeningEvidence.js';
/**
 * An incident observed in live traffic or reconstructed during replay.
 */
export type AlertObservedIncident = {
  opening_evidence?: AlertObservedIncidentOpeningEvidence;
  /**
   * Environment of this observation when scoped.
   */
  environment_scope?: string;
  opened_at: string;
  ended_at?: string;
  outcome?: 'recovered' | 'observation_ended';
  /**
   * Notification opportunities for this incident under the rule's cooldown.
   */
  eligible_notifications: number;
  last_eligible_at: string;
};

