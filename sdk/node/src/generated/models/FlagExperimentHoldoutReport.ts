/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagExperimentArm } from './FlagExperimentArm.js';
/**
 * Held-out customers against every other customer active in the range: the share that sent the event, their difference (exposed minus held) and a 95% interval.
 */
export type FlagExperimentHoldoutReport = {
  event: string;
  from: string;
  until: string;
  basis_points: number;
  held: FlagExperimentArm;
  exposed: FlagExperimentArm;
  difference: number;
  low: number;
  high: number;
  /**
   * True when the customer lists hit the analytics unit bound.
   */
  truncated: boolean;
};

