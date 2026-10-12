/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RealtimeConsumerGroupMessage } from './RealtimeConsumerGroupMessage.js';
/**
 * Realtime Consumer Group Claim.
 */
export type RealtimeConsumerGroupClaim = {
  message: RealtimeConsumerGroupMessage;
  worker: string;
  attempt: number;
  /**
   * Opaque lease credential. Never log or persist in generic response caches.
   */
  claim_token: string;
  lease_until: string;
  /**
   * Immutable counter namespace. A recreated group has a different UUID.
   */
  group_id: string;
  /**
   * Monotonically increasing per-group signed-64-bit counter. Stable on renewal, new on every new lease including retry/replay. Compare with BigInt, within a group UUID and resource scope; downstream storage must enforce stale-write rejection.
   */
  fencing_token: string;
  /**
   * Frozen deadline, or null. lease_until never extends beyond it. Handlers must stop cooperatively and guard external writes.
   */
  delivery_deadline: string | null;
};

