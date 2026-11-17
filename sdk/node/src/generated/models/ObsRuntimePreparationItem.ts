/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeHandoffCollection } from './ObsRuntimeHandoffCollection.js';
import type { ObsRuntimePreparationObservation } from './ObsRuntimePreparationObservation.js';
import type { ObsRuntimePreparationPhase } from './ObsRuntimePreparationPhase.js';
/**
 * One sanitized image worker recovery scope. Collection status, observation expectation and three ordered phase observations are validated independently; no build, app, or deployment identity is exposed.
 */
export type ObsRuntimePreparationItem = {
  node?: string;
  target_id?: string;
  unexpected?: boolean;
  scrape_observed_at?: string;
  state: 'available' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  collection: ObsRuntimeHandoffCollection;
  observation: ObsRuntimePreparationObservation;
  phases: Array<ObsRuntimePreparationPhase>;
};

