/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PreAuthFailedResponseLimit } from './PreAuthFailedResponseLimit.js';
/**
 * Optional stricter per-source limit for one public method and path, with optional shared request coordination and a response-based failure budget.
 */
export type PreAuthRouteLimit = {
  method: 'GET' | 'HEAD' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'OPTIONS';
  /**
   * Canonical absolute public URL path with no query, fragment, or percent encoding. Matching is exact after normalizing the decoded request path.
   */
  path: string;
  requests_per_second: number;
  burst: number;
  /**
   * Optional shared request budget across gateway replicas. Defaults to local. Central mode uses 1,024 opaque source shards per exact route; collisions share allowance. On database errors it falls back to the replica-local bucket. Failed-response coordination is configured separately.
   */
  coordination?: 'local' | 'central';
  failed_responses?: PreAuthFailedResponseLimit;
  /**
   * Observe repeated application login failures for the same app-declared target across source IPs. Requires POST, failed_responses, and central coordination. The application supplies X-Gregale-Abuse-Target as lowercase hex HMAC-SHA256 of the normalized submitted login identifier on selected failed responses; the gateway strips it before sending the response. This signal never rejects requests.
   */
  observe_targets?: boolean;
};

