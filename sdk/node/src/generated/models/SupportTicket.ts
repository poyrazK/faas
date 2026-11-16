/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SupportContext } from './SupportContext.js';
/**
 * Customer-visible case metadata; staff assignments, targets, notes and engineering records stay private.
 */
export type SupportTicket = {
  id: string;
  subject: string;
  category: string;
  status: 'open' | 'pending' | 'resolved';
  revision: number;
  context: SupportContext;
  created_at: string;
  updated_at: string;
  resolved_at?: string;
  /**
   * Unread support updates for the current caller and audience.
   */
  unread_count?: number;
  can_reopen: boolean;
  reopen_until?: string;
};

