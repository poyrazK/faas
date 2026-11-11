/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DomainBindingSpec } from './DomainBindingSpec.js';
/**
 * Versioned desired bindings. Only listed domains change; omission never removes a binding. At most 20 bindings in a 128 KiB request. Use a separate file from the application deployment manifest.
 */
export type DomainManifest = {
  version: 1;
  domains: Array<DomainBindingSpec>;
};

