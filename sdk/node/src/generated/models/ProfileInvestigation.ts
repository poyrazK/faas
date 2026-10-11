/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileInvestigationInput } from './ProfileInvestigationInput.js';
import type { ProfileRegressionAssessment } from './ProfileRegressionAssessment.js';
/**
 * Persistent app-owned selections and notes; no profile samples are stored. Up to 50 investigations per app.
 */
export type ProfileInvestigation = {
  id: string;
  app_id: string;
  revision: number;
  investigation: ProfileInvestigationInput;
  created_at: string;
  updated_at: string;
  assessment?: ProfileRegressionAssessment;
};

