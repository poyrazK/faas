/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileAttributionQuality } from './ProfileAttributionQuality.js';
/**
 * Captured route attribution quality comparison. An absolute labeled-share change of at least 20 percentage points suppresses advisory route regression conclusions without changing aggregate results or rollout behavior. Background work and traffic changes can also change labeled CPU share.
 */
export type ProfileAttributionComparison = {
  baseline?: ProfileAttributionQuality;
  candidate?: ProfileAttributionQuality;
  available: boolean;
  delta_percentage_points?: number;
  substantial_change: boolean;
  maximum_change_percentage_points: number;
  warnings: Array<string>;
};

