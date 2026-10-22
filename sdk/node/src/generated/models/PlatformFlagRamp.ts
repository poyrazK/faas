/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Advances the rule's rollout stage by stage (ADR 20261021080640). The rollout is the current stage; apid moves it on once the flag's version has held for interval_seconds and its health routes stayed under max_5xx_basis_points over that window with at least min_requests requests. On a breach it pauses (and for rollback returns one stage); a paused ramp waits for an operator to clear paused.
 */
export type PlatformFlagRamp = {
  /**
   * Strictly ascending basis points; the rollout must be one of them.
   */
  stages: Array<number>;
  interval_seconds: number;
  max_5xx_basis_points: number;
  min_requests: number;
  on_breach: 'pause' | 'rollback';
  paused?: boolean;
  paused_reason?: string;
};

