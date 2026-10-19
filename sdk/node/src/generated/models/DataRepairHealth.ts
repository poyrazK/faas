/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataRepairHealthItem } from './DataRepairHealthItem.js';
import type { DataRepairPhaseHealth } from './DataRepairPhaseHealth.js';
/**
 * Durable repair reservations and phase clocks; capacity is not measured host usage.
 */
export type DataRepairHealth = {
  observed_at: string;
  /**
   * All eight monitored phases, including zero counts.
   */
  phases: Array<DataRepairPhaseHealth>;
  held_slots: number;
  held_memory_mb: number;
  held_vcpus: number;
  held_cpu_millicores: number;
  unavailable_tasks: number;
  items: Array<DataRepairHealthItem>;
  /**
   * Omitted on the last page.
   */
  next_cursor?: string;
  limit: number;
};

