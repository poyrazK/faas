/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Asks browsers on matched routes to prove they run a real browser
 * before the request reaches the app (ADR-831 amendment 7). A request
 * without a valid pass is answered at the edge: a page load gets a page
 * that solves a short proof of work in the browser and sets a signed
 * pass cookie, and any other request gets 403 challenge_required.
 * Requests with a verified API key, consumer key or JWT subject are
 * never challenged. Use log mode to see what a rule would challenge,
 * and add a verified_bot missing condition so search crawlers pass.
 * Hobby and above. Omitted values are stored as their defaults.
 *
 */
export type EdgeRuleChallengeAction = {
  /**
   * Leading zero bits the browser must find in a SHA-256 hash. Each extra bit doubles the work; the default takes tens to a few hundred milliseconds.
   */
  difficulty?: number;
  /**
   * How long a solved challenge lets the browser through.
   */
  pass_ttl_seconds?: number;
  /**
   * When above difficulty, the gateway raises the difficulty up to this while the rule serves far more challenge pages than usual, two bits (4x the work) per minute, and lowers it again as the load eases (ADR-831 amendment 8). Must be at least difficulty. Omitted or equal to difficulty keeps the difficulty fixed.
   */
  max_difficulty?: number;
};

