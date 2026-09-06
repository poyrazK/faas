/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MirrorCleanCondition } from './MirrorCleanCondition.js';
/**
 * One stage of a customer-supplied canary ladder
 * (issue #976 / ADR-122 / SAFE-RELEASES production-leveling
 * Stream F). Percent is the traffic share this stage moves
 * to (0..100, terminal stage must be 100). Duration is the
 * wall-clock dwell time at this stage in time.ParseDuration
 * form (e.g. "30s", "2m", "0s" for the terminal hop).
 *
 */
export type CustomStage = {
  /**
   * Traffic share this stage moves to (0..100). The terminal stage must be 100.
   */
  percent: number;
  /**
   * Wall-clock dwell at this stage, in time.ParseDuration form (e.g. '30s', '2m'). '0s' for the terminal hop.
   */
  duration: string;
  /**
   * Require a clean traffic-mirror window before advancing out of this stage. Any status, schema, body, or crash diff aborts the rollout.
   */
  mirror_clean?: (MirrorCleanCondition | null);
};

