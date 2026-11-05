/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Metadata-only health hold, committed resume, or committed automatic abort event. Produced by actual enforced canary advance evaluations. Unknown evidence never clears a confirmed regression; a fresh context never claims recovery. App-scoped recipients are captured with the decision. Fetch history_path with apps:read and completed MFA for route evidence; snapshots are subject to retention. Repeated worker retries do not emit duplicate holds. Automatic abort events describe a committed recovery from fresh confirmed 5xx evidence under an opt-in policy; receiving a webhook never authorizes a traffic mutation.
 */
export type RouteHealthTransitionWebhookPayload = {
  version: 1;
  app_id: string;
  deployment_id: string;
  /**
   * Empty when a unique stable deployment was unavailable.
   */
  stable_deployment_id: string;
  decision_id: string;
  /**
   * The prior comparable hold decision for resumed or aborted events when a hold exists; may have been pruned.
   */
  blocked_decision_id?: string;
  status: 'blocked' | 'resumed' | 'aborted';
  health_status: 'unknown' | 'regressed' | 'healthy';
  reason: string;
  source: 'manual' | 'worker';
  canary_step: number;
  revision: number;
  observation_anchor?: string;
  checked_at: string;
  previous_traffic_percent: number;
  requested_traffic_percent: number;
  /**
   * Authenticated single-entry API path for the exact saved decision.
   */
  history_path: string;
};

