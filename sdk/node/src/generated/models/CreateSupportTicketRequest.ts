/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SupportContext } from './SupportContext.js';
/**
 * Submit a support request within the authenticated personal or organization scope.
 */
export type CreateSupportTicketRequest = {
  subject: string;
  category: 'deployment' | 'billing' | 'account' | 'incident' | 'other';
  body: string;
  priority?: 'low' | 'normal' | 'high' | 'urgent';
  context?: SupportContext;
};

