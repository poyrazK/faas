/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessVerificationWatchItem } from './OperationWorkflowReadinessVerificationWatchItem.js';
/**
 * Opt-in watch of captured resolution identities. Verified proofs remain observed for later loss or explicit blocker reopening; partial coverage and source inbox retention remain explicit.
 */
export type OperationWorkflowReadinessVerificationWatch = {
  /**
   * OperationWorkflowReadinessVerificationWatch.id. Server-managed advisory verification observation for the retained source completion.
   */
  id: string;
  /**
   * OperationWorkflowReadinessVerificationWatch.revision. Server-managed advisory verification observation for the retained source completion.
   */
  revision: number;
  /**
   * OperationWorkflowReadinessVerificationWatch.started_at. Server-managed advisory verification observation for the retained source completion.
   */
  started_at: string;
  /**
   * OperationWorkflowReadinessVerificationWatch.evaluated_at. Server-managed advisory verification observation for the retained source completion.
   */
  evaluated_at: string;
  /**
   * OperationWorkflowReadinessVerificationWatch.state_revision. Server-managed advisory verification observation for the retained source completion.
   */
  state_revision: number;
  /**
   * OperationWorkflowReadinessVerificationWatch.contract_version. Server-managed advisory verification observation for the retained source completion.
   */
  contract_version: number;
  /**
   * OperationWorkflowReadinessVerificationWatch.pending_count_at_start. Server-managed advisory verification observation for the retained source completion.
   */
  pending_count_at_start: number;
  /**
   * OperationWorkflowReadinessVerificationWatch.coverage_incomplete. Server-managed advisory verification observation for the retained source completion.
   */
  coverage_incomplete: boolean;
  /**
   * OperationWorkflowReadinessVerificationWatch.items. Server-managed advisory verification observation for the retained source completion.
   */
  items: Array<OperationWorkflowReadinessVerificationWatchItem>;
};

