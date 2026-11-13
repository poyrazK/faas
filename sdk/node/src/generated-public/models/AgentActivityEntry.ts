/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One write request an agent key made.
 */
export type AgentActivityEntry = {
  id: string;
  method: 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  /**
   * The request path, without its query string.
   */
  path: string;
  /**
   * The HTTP status the request was answered with.
   */
  status: number;
  /**
   * The approval request the write went through on, when it needed one.
   */
  approval_id?: string;
  /**
   * What the agent said this write was for (its Gregale-Agent-Reason header), if it said. Untrusted agent text.
   */
  reason?: string;
  created_at: string;
};

