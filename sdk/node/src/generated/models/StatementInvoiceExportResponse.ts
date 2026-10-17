/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BillingProvider } from './BillingProvider.js';
/**
 * A finalized statement's invoice in the account's billing provider (ADR-1502, ADR-1507).
 */
export type StatementInvoiceExportResponse = {
  statement_id: string;
  provider: BillingProvider;
  customer: string;
  /**
   * creating means an earlier attempt stopped part-way; repeat the export to resume it.
   */
  status: 'creating' | 'completed';
  invoice_id?: string;
  dashboard_url?: string;
  currency: string;
  /**
   * Invoice total in the currency's minor unit (cents): the statement total rounded to the cent.
   */
  amount_minor: number;
  livemode: boolean;
  /**
   * What the provider was asked to do with the invoice when the export began (ADR-1311).
   */
  collection?: 'none' | 'send' | 'charge';
  /**
   * Whether Stripe Tax calculated this invoice's tax (ADR-1313).
   */
  automatic_tax?: boolean;
  created_at: string;
  completed_at?: string;
  source_kind?: 'app_consumer' | 'platform_tenant';
  /**
   * The provider invoice's status as last read by Gregale (ADR-1504, ADR-1507); deleted means the provider no longer has it. Absent until the first sync.
   */
  payment_status?: 'draft' | 'open' | 'paid' | 'void' | 'uncollectible' | 'deleted';
  amount_paid_minor?: number;
  amount_remaining_minor?: number;
  due_at?: string;
  paid_at?: string;
  /**
   * The provider's customer-facing invoice or checkout page once there is one.
   */
  hosted_invoice_url?: string;
  /**
   * Open, past its due date, with an amount remaining.
   */
  overdue?: boolean;
  status_synced_at?: string;
};

