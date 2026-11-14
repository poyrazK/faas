/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DomainBindingChange } from './DomainBindingChange.js';
import type { DomainManifest } from './DomainManifest.js';
/**
 * Deterministic review artifact. The SHA-256 binds the diff to private claim identity, intervening moves, destination release and traffic allocations. It contains no ownership proof token.
 */
export type DomainPlan = {
  version?: 1;
  sha256?: string;
  manifest?: DomainManifest;
  changes?: Array<DomainBindingChange>;
};

