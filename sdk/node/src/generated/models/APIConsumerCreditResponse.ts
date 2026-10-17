/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One prepaid credit grant.
 */
export type APIConsumerCreditResponse = {
  id: string;
  consumer_id: string;
  currency: string;
  amount_millicents: number;
  remaining_millicents: number;
  status: 'active' | 'used' | 'expired' | 'voided';
  note?: string;
  expires_at?: string;
  voided_at?: string;
  created_at: string;
};

