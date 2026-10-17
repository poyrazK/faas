/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioObjectsMutation } from './ScenarioObjectsMutation.js';
import type { ScenarioObjectsObject } from './ScenarioObjectsObject.js';
import type { ScenarioObjectsReceipt } from './ScenarioObjectsReceipt.js';
import type { ScenarioObjectsSpec } from './ScenarioObjectsSpec.js';
/**
 * Independently collected object state and mutation receipts, published atomically with content cleanup.
 */
export type ScenarioObjectsEvidence = (ScenarioObjectsReceipt & {
  status: 'complete';
  provenance: 'platform_object_collector';
  deployment_id: string;
  spec: ScenarioObjectsSpec;
  before: Array<ScenarioObjectsObject>;
  after: Array<ScenarioObjectsObject>;
  mutations: Array<ScenarioObjectsMutation>;
  /**
   * The original collector acknowledged a closed capture window.
   */
  collector_closed: boolean;
  cleanup_confirmed: boolean;
  reason?: string;
});

