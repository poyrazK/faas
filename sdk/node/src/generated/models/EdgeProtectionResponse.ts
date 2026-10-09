/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Edge rejection counts for one app over a range.
 */
export type EdgeProtectionResponse = {
  app_id: string;
  range: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d';
  /**
   * prometheus or degraded: <reason>.
   */
  source: string;
  as_of: string;
  pre_auth: {
    /**
     * Requests the pre-auth source limit rejected in enforce mode.
     */
    blocked: number;
    /**
     * Requests observe mode would have rejected.
     */
    would_block: number;
  };
  /**
   * kind=validate mismatches by validate_mode (block, observe, warn); zero counts are omitted.
   */
  validation_failures: Array<{
    name: string;
    count: number;
  }>;
  /**
   * Requests answered by an edge gate, largest count first; zero counts are omitted.
   */
  rejections: Array<{
    gate: 'jwt' | 'ip_allowlist' | 'internal_only' | 'ip' | 'geo' | 'limit' | 'body_limit' | 'throttle';
    status: '401' | '403' | '413' | '429' | '503' | 'other';
    count: number;
  }>;
  /**
   * kind=waf inspections (observe-only, ADR-831 step 1). Detections are not rejections.
   */
  waf: {
    /**
     * Requests scored by the OWASP CRS.
     */
    inspected: number;
    /**
     * Inspected requests at or above the rule's anomaly threshold.
     */
    detected: number;
    /**
     * Matched requests skipped to protect the node (budget, full queue, or error).
     */
    not_inspected: number;
    /**
     * Detections by CRS attack category, largest first; one detection may count several.
     */
    categories: Array<{
      name: string;
      count: number;
    }>;
    /**
     * CRS rule IDs that scored most, largest first, for tuning exclude_rule_ids.
     */
    top_rules: Array<{
      /**
       * CRS rule ID.
       */
      name: string;
      count: number;
    }>;
  };
};

