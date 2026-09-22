/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppPrivateNetworkNodeStatus } from './AppPrivateNetworkNodeStatus.js';
/**
 * Provider-neutral private-network attachment intent for one app.
 * `pending` and `error` are fail-closed; only `ready` admits private
 * network traffic after a connector has reconciled the request.
 *
 */
export type AppPrivateNetworkAttachment = {
  id: string;
  network_id: string;
  region: string;
  cidrs: Array<string>;
  /**
   * Optional private-network policy. Empty preserves allow-all behavior; populated ranges are admitted symmetrically for private egress and ingress.
   */
  allowed_cidrs?: Array<string>;
  /**
   * Stable Gregale member address for this app when the fabric is enabled.
   */
  address?: string;
  status: 'pending' | 'ready' | 'error';
  status_detail?: string;
  /**
   * Last durable convergence result for each compute node serving this attachment.
   */
  nodes?: Array<AppPrivateNetworkNodeStatus>;
  created_at?: string;
  updated_at?: string;
};

