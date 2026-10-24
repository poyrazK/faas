/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CanaryPresetSpec } from './CanaryPresetSpec.js';
/**
 * When and how a release waiting without traffic starts its rollout (ADR-20261021152201).
 */
export type ScheduleReleaseRequest = {
  /**
   * When the rollout starts; at most 7 days ahead. A time that has passed starts it on apid's next pass.
   */
  release_at: string;
  canary?: CanaryPresetSpec;
  notify_channels?: Array<string>;
  /**
   * Release into the deploy freezes active at release_at; audited when the release starts.
   */
  override_freeze?: string;
};

