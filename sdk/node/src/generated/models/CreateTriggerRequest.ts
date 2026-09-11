/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FilterCriteria } from './FilterCriteria.js';
import type { TriggerKind } from './TriggerKind.js';
/**
 * Trigger create payload. Kind is immutable after create. Per-kind
 * gating mirrors pkg/gregalemanifest.validateKindConfig:
 * - cron: requires schedule + path (slug ignored)
 * - non-cron: requires slug + config
 * Omitted delivery settings use the platform default capped to the
 * selected account plan; explicit over-cap values are rejected.
 *
 */
export type CreateTriggerRequest = {
  app_id: string;
  kind: TriggerKind;
  slug?: string;
  enabled?: boolean | null;
  /**
   * Per-kind opaque config blob. Kafka password and client_key leaves are plaintext-on-write and sealed before persistence.
   */
  config?: Record<string, any>;
  batch_size_max?: number | null;
  batch_window_ms?: number | null;
  max_attempts?: number | null;
  payload_max_bytes?: number | null;
  /**
   * Kafka-only poison-record handling strategy. null/omitted
   * falls through to the DB default 'commit'. Same semantics
   * as the Trigger read shape.
   *
   */
  broker_poison_strategy?: 'commit' | 'seek-to-offset';
  /**
   * Optional record-level filter (ADR-118 §6). nil on
   * create falls through to the DB default NULL — every
   * record passes the filter.
   *
   */
  filter_criteria?: FilterCriteria;
  schedule?: string | null;
  path?: string | null;
};

