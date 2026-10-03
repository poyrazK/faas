/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagsBundle } from './FlagsBundle.js';
/**
 * Configuration publication with audit metadata; version zero is an unpublished empty environment.
 */
export type FeatureFlagVersion = (FlagsBundle & {
  /**
   * Publishing account identity.
   */
  actor: string;
  created_at: string;
  restored_from?: number;
});

