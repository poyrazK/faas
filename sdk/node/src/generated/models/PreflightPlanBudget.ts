/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * What one plan includes, expressed as running time. Preflight does not
 * estimate an app's memory use, so it reports each tier's allowance
 * rather than recommending one.
 *
 */
export type PreflightPlanBudget = {
  plan: string;
  ram_mb: number;
  /**
   * Plan RAM plus the fixed per-VM overhead.
   */
  billed_ram_mb: number;
  /**
   * The included allowance as wall-clock running time at this plan's billed RAM.
   */
  included_running_minutes: number;
  included_gb_hours: number;
  /**
   * Monthly subscription price in millicents.
   */
  price_millicents: number;
  overage_millicents_per_gb_hour?: number;
};

