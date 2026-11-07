/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One request of an agent connection that needed a person's approval.
 */
export type AgentApprovalResponse = {
  /**
   * The request's id.
   */
  id: string;
  /**
   * The kind of action.
   */
  category: 'delete' | 'production' | 'credentials' | 'billing';
  /**
   * The HTTP method of the request the approval allows.
   */
  method: string;
  /**
   * The path and query of the request the approval allows.
   */
  path: string;
  /**
   * Whether the approval is tied to the request body; large and multipart bodies are not.
   */
  body_bound: boolean;
  /**
   * `consumed` once the approved request was made.
   */
  status: 'pending' | 'approved' | 'denied' | 'consumed' | 'expired';
  /**
   * The review page
   */
  review_url?: string;
  /**
   * The connection's client name or key label.
   */
  agent?: string;
  /**
   * When the agent first made the request.
   */
  created_at: string;
  /**
   * After this time the request can no longer be approved or used.
   */
  expires_at: string;
  /**
   * When a person approved or denied it.
   */
  decided_at?: string;
};

