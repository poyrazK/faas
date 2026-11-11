/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An agent connection's monthly budget for always-on capacity, and what it committed this month (ADR-20261111114059).
 */
export type AgentBudgetResponse = {
  /**
   * The budget that applies, in cents a month; absent when unlimited.
   */
  monthly_cents?: number;
  /**
   * Where the budget comes from.
   */
  source: 'key' | 'plan_default' | 'org_ceiling' | 'unlimited';
  /**
   * The budget set for this agent, when one was.
   */
  key_monthly_cents?: number;
  /**
   * The agent was set to have no budget of its own.
   */
  key_unlimited?: boolean;
  /**
   * The organization's ceiling on agents' budgets, when it set one.
   */
  org_max_cents?: number;
  /**
   * Always-on capacity the agent added this UTC month, priced at the overage rate and rounded up to the cent.
   */
  spent_cents: number;
  /**
   * The UTC month spend counts in.
   */
  month: string;
};

