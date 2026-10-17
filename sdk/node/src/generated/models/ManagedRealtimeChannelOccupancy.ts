/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Fleet-wide subscription and presence counts for one channel.
 */
export type ManagedRealtimeChannelOccupancy = {
  channel: string;
  /**
   * Open subscriptions on every transport (WebSocket
   */
  subscribers: number;
  /**
   * Distinct presence members; a principal-scoped member counts once.
   */
  presence_members: number;
};

