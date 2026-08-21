/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row of the alert-preset catalog (issue #1233, ADR-123).
 * The catalog is system-seeded and R/O for customers — the
 * enable endpoint clones the row into a real alert_rules row
 * the customer owns from then on. No persistent preset_id FK
 * lands on alert_rules.
 *
 */
export type AlertPresetResponse = {
  id: string;
  name: string;
  display_name: string;
  description: string;
  category: 'availability' | 'reliability' | 'cost' | 'deployment' | 'infrastructure';
  metric: string;
  comparison: 'gt' | 'gte' | 'lt' | 'lte';
  threshold: number;
  window_spec: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d';
  default_cooldown_minutes: number;
  minimum_plan: 'free' | 'hobby' | 'pro' | 'scale';
  enabled_in_catalog: boolean;
};

