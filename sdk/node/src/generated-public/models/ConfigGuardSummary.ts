/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The config guard an env or secret write opened or joined (ADR-1451).
 */
export type ConfigGuardSummary = {
  id: string;
  status: 'watching' | 'clean' | 'restored' | 'regressed' | 'no_traffic' | 'superseded';
  base_revision: number;
  window_minutes?: number;
};

