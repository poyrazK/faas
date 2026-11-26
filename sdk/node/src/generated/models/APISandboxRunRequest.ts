/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type APISandboxRunRequest = {
  scenario_revision: string;
  mode?: 'automated' | 'partner';
  /**
   * Use the receiver registered for this outbound run.
   */
  partner_webhook?: boolean;
};

