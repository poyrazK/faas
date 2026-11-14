/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DomainManifest } from './DomainManifest.js';
/**
 * Apply exactly the reviewed bindings. Confirmation and the fingerprint from planDomains are required.
 */
export type DomainApplyRequest = {
  manifest: DomainManifest;
  expected_plan_sha256: string;
  confirm: boolean;
};

