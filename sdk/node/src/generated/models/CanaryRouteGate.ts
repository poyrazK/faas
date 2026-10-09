/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-app gate for canary route-requirements evidence and production lifecycle declarations. Absence is report mode with revision 0. Lifecycle enforcement includes initial positive activation, promotions, traffic redistribution and ordinary rollback. Dark staging, validated abort and automatic incident recovery remain available.
 */
export type CanaryRouteGate = {
  app_id: string;
  mode: 'report' | 'enforce';
  revision: number;
  updated_at?: string;
};

