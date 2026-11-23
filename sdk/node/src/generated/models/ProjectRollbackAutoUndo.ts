/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { Problem } from './Problem.js';
/**
 * The trigger and outcome of an automatic undo after a confirmed health failure.
 */
export type ProjectRollbackAutoUndo = {
  status: 'pending' | 'ready' | 'started' | 'blocked';
  /**
   * Service with the confirmed 5xx failure.
   */
  trigger_app_id: string;
  /**
   * Identifies a live_traffic breach. Omitted for a health probe failure.
   */
  trigger?: string;
  /**
   * Applied route generation for the production breach.
   */
  route_generation?: number;
  /**
   * Start of the original live-traffic breach window.
   */
  window_start?: string;
  /**
   * End of the original live-traffic breach window.
   */
  window_end?: string;
  problem?: Problem;
};

