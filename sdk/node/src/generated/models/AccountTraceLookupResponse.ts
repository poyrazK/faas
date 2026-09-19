/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountTraceInvocation } from './AccountTraceInvocation.js';
import type { AccountTraceLookupError } from './AccountTraceLookupError.js';
import type { AccountTraceMatch } from './AccountTraceMatch.js';
import type { DebugTelemetrySpan } from './DebugTelemetrySpan.js';
/**
 * Tenant-scoped distributed-trace correlation envelope.
 */
export type AccountTraceLookupResponse = {
  trace_id: string;
  generated_at: string;
  limit: number;
  matches: Array<AccountTraceMatch>;
  invocations: Array<AccountTraceInvocation>;
  spans: Array<DebugTelemetrySpan>;
  spans_truncated: boolean;
  partial?: boolean;
  errors?: Array<AccountTraceLookupError>;
};

