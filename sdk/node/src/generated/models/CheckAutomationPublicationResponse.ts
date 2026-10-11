/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationCheckEvidence } from './AutomationCheckEvidence.js';
/**
 * Expiring server receipt with safe verified scenario and coverage metadata.
 */
export type CheckAutomationPublicationResponse = {
  /**
   * Opaque 30-minute receipt bound to app, name, draft, actor and policy version. Do not log or expose it.
   */
  receipt: string;
  expires_at: string;
  evidence: AutomationCheckEvidence;
};

