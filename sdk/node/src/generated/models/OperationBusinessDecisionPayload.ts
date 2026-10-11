/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationBusinessDecision } from './OperationBusinessDecision.js';
/**
 * Versioned application decision evidence carried in a declared milestone payload. Must match its declared workflow step and instance. Uses existing milestone transaction, publication, ownership, and retention boundaries.
 */
export type OperationBusinessDecisionPayload = {
  kind: 'gregale.business-decision.v1';
  decision: OperationBusinessDecision;
};

