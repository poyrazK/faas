/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugRequestCorrelationStage } from './DebugRequestCorrelationStage.js';
/**
 * Fixed-shape edge-to-billing correlation for a retained request. Every stage is present so unavailable telemetry is visible.
 */
export type DebugRequestCorrelation = {
  stages: Array<DebugRequestCorrelationStage>;
  /**
   * True only when every applicable stage has complete retained evidence.
   */
  complete: boolean;
};

