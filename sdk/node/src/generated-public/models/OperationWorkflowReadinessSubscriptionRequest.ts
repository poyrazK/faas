/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowNextActionDigestPolicy } from './OperationWorkflowNextActionDigestPolicy.js';
import type { OperationWorkflowReadinessImpactWatchRequest } from './OperationWorkflowReadinessImpactWatchRequest.js';
/**
 * Create a durable readiness inbox subscription; initial observations establish a baseline without inventing readiness changes.
 */
export type OperationWorkflowReadinessSubscriptionRequest = {
  /**
   * Optional team digest policy using this subscription customer/environment; filters are independent of transition discovery. Policy is fixed until subscription deletion.
   */
  next_action_digest?: OperationWorkflowNextActionDigestPolicy;
  /**
   * OperationWorkflowReadinessSubscriptionRequest.business_impact_watches. Optional unique workflow/result watches in the subscription application, customer and environment scope. Initial successful observation is silent; configuration remains fixed until deletion. These targets are independent of transition discovery owner/operation filters.
   */
  business_impact_watches?: Array<OperationWorkflowReadinessImpactWatchRequest>;
  /**
   * OperationWorkflowReadinessSubscriptionRequest.app_id. Application UUID; required in customer requests and omitted in account requests.
   */
  app_id?: string;
  /**
   * OperationWorkflowReadinessSubscriptionRequest.tenant_id. Optional account-only customer selector; customer identity derives from authentication.
   */
  tenant_id?: string;
  /**
   * OperationWorkflowReadinessSubscriptionRequest.scope. Application environment scope.
   */
  scope: string;
  /**
   * OperationWorkflowReadinessSubscriptionRequest.name. Subscription name.
   */
  name: string;
  /**
   * OperationWorkflowReadinessSubscriptionRequest.workflow. Optional declared workflow selector.
   */
  workflow?: string;
  /**
   * OperationWorkflowReadinessSubscriptionRequest.target_operation. Optional transition operation selector.
   */
  target_operation?: string;
  /**
   * OperationWorkflowReadinessSubscriptionRequest.owner. Optional blocker or pending verification owner selector.
   */
  owner?: string;
  /**
   * OperationWorkflowReadinessSubscriptionRequest.unassigned. Select ownerless blockers or verification obligations.
   */
  unassigned?: boolean;
  /**
   * OperationWorkflowReadinessSubscriptionRequest.events. Readiness inbox event selectors; defaults to all four when omitted.
   */
  events?: Array<'newly_ready' | 'newly_blocked' | 'reassigned' | 'unavailable'>;
};

