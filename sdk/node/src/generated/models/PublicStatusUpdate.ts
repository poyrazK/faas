/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One append-only, plain-text event timeline entry.
 */
export type PublicStatusUpdate = {
  id: string;
  state: 'investigating' | 'identified' | 'monitoring' | 'resolved' | 'scheduled' | 'in_progress' | 'completed' | 'cancelled';
  message: string;
  posted_at: string;
};

