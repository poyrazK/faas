/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CustomStage } from './CustomStage.js';
/**
 * The canary ladder a customer asks for on a deploy (issue
 * #976 / ADR-122 / SAFE-RELEASES-A + production-leveling
 * Stream F). Preset is the catalog name from
 * pkg/api/canary (none/slow/balanced/aggressive/1-10-50-100/adaptive/
 * custom). When Preset is 'custom', Stages is the
 * customer-supplied ladder (each entry is percent +
 * duration string in time.ParseDuration form, e.g.
 * "1% at 30s, 10% at 2m, 100% at 0s").
 * The wire-format change (StepDurations removed, Stages
 * added) is additive on the consumer side because the
 * prior StepDurations field was declared-but-dead — no
 * pre-PR client ever sent it.
 *
 */
export type CanaryPresetSpec = {
  /**
   * Catalog preset name. 'none' = no canary (server stamps canary_preset='none', canary_total_steps=0). 'custom' requires Stages to be non-empty.
   */
  preset: 'none' | 'slow' | 'balanced' | 'aggressive' | '1-10-50-100' | 'adaptive' | 'custom';
  /**
   * Per-stage ladder. Required when preset='custom' (the apid handler 422s otherwise); ignored for catalog presets (the catalog resolution runs server-side).
   */
  stages?: Array<CustomStage>;
};

