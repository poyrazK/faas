/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeAttackModeStatus } from './EdgeAttackModeStatus.js';
import type { EdgeProtectionAICrawler } from './EdgeProtectionAICrawler.js';
/**
 * Edge rejection counts for one app over a range.
 */
export type EdgeProtectionResponse = {
  app_id: string;
  range: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d';
  /**
   * Where the edge counts come from: prometheus, or degraded: <reason> when they are unavailable (counts are then zero, not measured).
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
    gate: 'jwt' | 'ip_allowlist' | 'internal_only' | 'ip' | 'geo' | 'limit' | 'body_limit' | 'throttle' | 'waf' | 'challenge' | 'penalty';
    status: '401' | '403' | '413' | '429' | '503' | 'other';
    count: number;
  }>;
  /**
   * kind=waf inspections. Sampled detections are never blocked; block rules' 403s also appear in rejections under gate waf.
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
     * Requests a warn rule detected in-path and tagged with X-WAF-Warning.
     */
    warned: number;
    /**
     * Requests a block rule detected in-path and answered with 403.
     */
    blocked: number;
    /**
     * Requests that passed a warn or block rule unchecked because the in-path budget was exhausted.
     */
    inline_skipped: number;
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
  /**
   * kind=challenge decisions; its 403s also appear in rejections under gate challenge.
   */
  challenge: {
    /**
     * Challenge pages served to browser navigations.
     */
    issued: number;
    /**
     * Passes earned by solving the proof of work.
     */
    solved: number;
    /**
     * Non-navigation requests answered 403 challenge_required.
     */
    rejected: number;
    /**
     * Requests let through on a pass from an earlier solve.
     */
    passed: number;
    /**
     * Requests let through on a verified API key, consumer key or JWT.
     */
    bypassed: number;
    /**
     * Solutions refused: wrong work or an expired token.
     */
    invalid: number;
    /**
     * The part of issued served at a difficulty raised under attack.
     */
    raised?: number;
  };
  /**
   * Penalty boxes; refusals also appear in rejections under gate penalty.
   */
  penalty: {
    /**
     * Clients a rule's penalty boxed.
     */
    boxed: number;
    /**
     * Requests refused from boxed clients.
     */
    rejected: number;
  };
  attack_mode: EdgeAttackModeStatus;
  /**
   * Requests whose User-Agent names an AI crawler, largest first, counted before any edge rule acted on them.
   */
  ai_crawlers: Array<EdgeProtectionAICrawler>;
};

