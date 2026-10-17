/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable simulated relay attempt recorded before response delivery. This is not proof of email delivery or payment. Headers are excluded. Payload contains declared JSON fields. Hashes identify the canonical full request and idempotency key.
 */
export type ScenarioSimulationAttempt = {
  id: string;
  workload: string;
  fixture: string;
  integration: string;
  adapter: 'http' | 'resend';
  method: string;
  path: string;
  request_sha256: string;
  idempotency_sha256?: string;
  /**
   * Captured top-level JSON fields selected by the fixture.
   */
  payload: any;
  attempt: number;
  status: 'pending' | 'completed' | 'interrupted' | 'unsupported';
  response_status: number;
  fault?: 'timeout' | 'disconnect';
};

