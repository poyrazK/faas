/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FilterCriteria } from './FilterCriteria.js';
/**
 * Partial trigger update. nil means "leave unchanged" (same
 * semantics as UpdateCronRequest). Kind is NOT a member — it
 * is immutable. To change kind, create a new trigger and
 * delete the old one. Inside a supplied Kafka sasl/tls block,
 * omitting the write-only credential preserves its stored value;
 * omitting the whole block removes that block and its credential.
 *
 */
export type UpdateTriggerRequest = {
  enabled?: boolean | null;
  config?: any | null;
  batch_size_max?: number | null;
  batch_window_ms?: number | null;
  max_attempts?: number | null;
  payload_max_bytes?: number | null;
  /**
   * Kafka-only poison-record handling strategy. null/omitted
   * means "leave unchanged" (the SQL coalesce() in
   * pkg/state/pgstore.go::UpdateTrigger keeps the existing
   * value). Same semantics as the Trigger read shape.
   *
   */
  broker_poison_strategy?: 'commit' | 'seek-to-offset';
  /**
   * Optional record-level filter. nil means "leave
   * unchanged" — the SQL coalesce() in
   * pkg/state/pgstore.go::UpdateTrigger keeps the existing
   * value. Pass {} to clear an existing filter.
   *
   */
  filter_criteria?: FilterCriteria;
  schedule?: string | null;
  path?: string | null;
};

