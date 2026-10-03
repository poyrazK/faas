/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-app canary advance gate. Absence is report mode with revision 0. Initial activation, stable rollbacks and aborts are outside this gate.
 */
export type CanaryRouteGate = {
  app_id: string;
  mode: 'report' | 'enforce';
  revision: number;
  updated_at?: string;
};

