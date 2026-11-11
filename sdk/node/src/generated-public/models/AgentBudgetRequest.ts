/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sets an agent connection's budget: exactly one of monthly_cents, unlimited or default.
 */
export type AgentBudgetRequest = {
  /**
   * A monthly budget in cents.
   */
  monthly_cents?: number;
  /**
   * No budget of its own; the organization's ceiling still applies.
   */
  unlimited?: boolean;
  /**
   * Return to the plan's default budget.
   */
  default?: boolean;
};

