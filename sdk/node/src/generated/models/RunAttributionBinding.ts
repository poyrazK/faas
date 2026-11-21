/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RunBindingKind } from './RunBindingKind.js';
/**
 * Saved attribution binding and its current revision.
 */
export type RunAttributionBinding = {
  enabled: boolean;
  subject_kind: RunBindingKind;
  subject_id: string;
  customer_ref: string;
  agent_ref?: string;
  revision: number;
};

