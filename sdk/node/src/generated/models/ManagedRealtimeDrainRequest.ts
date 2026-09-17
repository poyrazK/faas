/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded and auditable selection for closing live connections.
 */
export type ManagedRealtimeDrainRequest = {
  /**
   * Only select connections subscribed to this channel.
   */
  channel?: string;
  /**
   * Only select connections for this authenticated principal.
   */
  principal?: string;
  /**
   * Explicit connection IDs to select in addition to the filters.
   */
  connection_ids?: Array<string>;
  /**
   * Maximum number of connections to select.
   */
  limit?: number;
  /**
   * Required reason recorded in the audit event and close request.
   */
  reason: string;
  /**
   * Return the selection without closing any connection.
   */
  dry_run?: boolean;
  /**
   * Permit closing the reachable subset when some nodes are unavailable.
   */
  allow_partial?: boolean;
};

