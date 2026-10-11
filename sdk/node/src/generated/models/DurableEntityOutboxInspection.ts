/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DurableEntityHeadDelivery } from './DurableEntityHeadDelivery.js';
/**
 * Pending outgoing work and retry metadata for the current queue head.
 */
export type DurableEntityOutboxInspection = {
  pending: number;
  head_id?: string;
  attempts: number;
  next_attempt_at?: string;
  exhausted: boolean;
  head_delivery?: DurableEntityHeadDelivery;
};

