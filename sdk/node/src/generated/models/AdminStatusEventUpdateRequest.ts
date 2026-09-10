/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Operator request to append a plain-text lifecycle update.
 */
export type AdminStatusEventUpdateRequest = {
  state: 'investigating' | 'identified' | 'monitoring' | 'resolved' | 'scheduled' | 'in_progress' | 'completed' | 'cancelled';
  message: string;
};

