/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DailyUsagePoint } from './DailyUsagePoint.js';
/**
 * Account-level monthly roll-up: included GB-hours, used, overage math, remaining balance, informational usage dimensions, and a trailing 30-day daily trend (issue #308). The GB-hours fields drive the overage math; the other dimensions are informational.
 */
export type UsageSummaryResponse = {
  month: string;
  used_gb_hours: number;
  included_gb_hours: number;
  overage_gb_hours: number;
  /**
   * Integer cents. Includes compute at €0.01/GB-h and configured live egress overage; shadow egress is excluded.
   */
  overage_cents: number;
  /**
   * Per-month CPU-hours (informational; not billed). issue #279 / PR-B.
   */
  used_cpu_hours?: number;
  /**
   * Per-month canonical interface egress GB. Σ net_tx_bytes across all apps; tx_bytes is a diagnostic subset and is not added. Informational while egress_billing_mode is absent.
   */
  used_egress_gb?: number;
  /**
   * Absent when egress billing is off. Shadow quantities are audited locally but are not sent to Polar.
   */
  egress_billing_mode?: 'shadow' | 'live';
  /**
   * Explicit non-retroactive UTC-hour activation boundary.
   */
  egress_billing_from?: string;
  /**
   * UTC-calendar-month included egress in provider billing units (GiB for Polar). Present when egress billing is configured.
   */
  included_egress_gb?: number;
  /**
   * Current monthly egress above the included amount. Shadow values are not charged.
   */
  egress_overage_gb?: number;
  /**
   * Configured price per egress billing unit in millicents.
   */
  egress_millicents_per_gb?: number;
  /**
   * Per-month ingress GB (informational; not billed). Σ net_rx_bytes across all apps, converted to GB. ADR-048. Mirror of `used_egress_gb` for the inbound direction.
   */
  used_ingress_gb?: number;
  /**
   * Per-month sum of WAKE_RESTORE→WAKE_COLD_BOOT transitions across every app on the account (informational; not billed). ADR-048.
   */
  cold_boots?: number;
  /**
   * Trailing 30 UTC calendar days, oldest first, grouped across the account. Empty when no daily rollup rows exist. issue #308.
   */
  daily?: Array<DailyUsagePoint>;
};

