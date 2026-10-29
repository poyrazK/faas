/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An app's attack mode. While it is on, every
 * request without a challenge pass is challenged: browsers solve a
 * short proof of work once and continue, verified search crawlers and
 * requests with a verified API key, consumer key or JWT pass straight
 * through, and other clients get 403 `challenge_required`. It always
 * ends on its own at `expires_at`. Read from the rules, so it is
 * current even when the edge-protection counts are degraded.
 *
 */
export type EdgeAttackModeStatus = {
  /**
   * Attack mode is on now.
   */
  active: boolean;
  /**
   * The app-wide challenge rule that carries attack mode (managed_by attack_mode). Still set after attack mode ran out, until it is switched on again or off.
   */
  rule_id?: string;
  /**
   * When attack mode was switched on; extending it keeps this time.
   */
  started_at?: string;
  /**
   * When attack mode ends
   */
  expires_at?: string;
  /**
   * Leading zero bits the browser's proof of work must find.
   */
  difficulty?: number;
  /**
   * Seconds a browser that solved the attack-mode challenge is let through.
   */
  pass_ttl_seconds?: number;
};

