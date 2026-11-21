/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An agent connection's monthly budget for always-on capacity, and the always-on cost its changes keep running.
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
   * Always-on cost a month that the agent's changes keep running, priced at the overage rate and rounded up to the cent. It does not reset each month; lowering capacity gives budget back.
   */
  spent_cents: number;
  /**
   * The current UTC month (kept for compatibility; spent_cents no longer resets monthly).
   */
  month: string;
};

