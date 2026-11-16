/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIPromptCanary } from './OutboundAIPromptCanary.js';
/**
 * A managed AI prompt. live_version serves unpinned calls; previous_live_version is what a rollback restores.
 */
export type OutboundAIPrompt = {
  id: string;
  name: string;
  description: string;
  live_version: number | null;
  previous_live_version: number | null;
  latest_version: number;
  canary: (OutboundAIPromptCanary | null);
  created_at: string;
  updated_at: string;
};

