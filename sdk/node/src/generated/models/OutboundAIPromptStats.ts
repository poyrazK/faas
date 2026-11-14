/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIPromptVersionStats } from './OutboundAIPromptVersionStats.js';
/**
 * A managed AI prompt's per-version stats.
 */
export type OutboundAIPromptStats = {
  window_start: string;
  live_version: number | null;
  canary_version: number | null;
  versions: Array<OutboundAIPromptVersionStats>;
};

