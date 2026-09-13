/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current error-budget indicator with its unit, target, and comparison direction.
 */
export type PublicStatusIndicator = {
  id: 'api_availability' | 'wake_p95' | 'build_success';
  label: string;
  value: number | null;
  unit: string;
  target: number;
  comparison: 'gte' | 'lte';
};

