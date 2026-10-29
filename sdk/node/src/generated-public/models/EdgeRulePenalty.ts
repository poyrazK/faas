/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A rule's penalty box. Once the rule has refused
 * the same client `after` times within `within_seconds`, every request
 * from that client to the app is refused with 403 `penalty_box` and
 * Retry-After for `seconds`, whichever rule or path it targets. The
 * client is its IP address (an IPv6 client is its /64). Boxes are held
 * in each gateway node's memory. Allowed on ip, geo, throttle and waf
 * (block mode) rules; editing the rule's penalty away, disabling it or
 * putting it in log mode releases its clients.
 *
 */
export type EdgeRulePenalty = {
  /**
   * How long a boxed client is refused.
   */
  seconds: number;
  /**
   * Refusals by this rule that box the client.
   */
  after?: number;
  /**
   * Window the refusals count in.
   */
  within_seconds?: number;
};

