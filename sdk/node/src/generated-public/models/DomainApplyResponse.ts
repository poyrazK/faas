/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CustomDomainResponse } from './CustomDomainResponse.js';
import type { DomainManifest } from './DomainManifest.js';
import type { DomainPlan } from './DomainPlan.js';
/**
 * The committed snapshot and optional previous bindings for a separately reviewed rollback.
 */
export type DomainApplyResponse = {
  plan?: DomainPlan;
  domains?: Array<CustomDomainResponse>;
  rollback?: DomainManifest;
};

