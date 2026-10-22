/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CanaryPresetSpec } from './CanaryPresetSpec.js';
/**
 * A release built ahead of time that starts its rollout at release_at (ADR-20261021152201).
 */
export type ScheduledRelease = {
  id: string;
  app: string;
  deployment_id: string;
  revision?: number;
  release_at: string;
  state: 'scheduled' | 'started' | 'blocked' | 'expired' | 'cancelled' | 'superseded';
  canary?: CanaryPresetSpec;
  notify_channels: Array<string>;
  /**
   * The reason given for releasing into the freezes in overridden_freezes.
   */
  override_freeze?: string;
  overridden_freezes?: Array<string>;
  /**
   * Why a schedule ended without starting, such as release_frozen or not_ready.
   */
  outcome_code?: string;
  outcome_detail?: string;
  scheduled_by: string;
  created_at: string;
  updated_at: string;
  started_at?: string;
};

