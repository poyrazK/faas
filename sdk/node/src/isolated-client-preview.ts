import * as services from './generated/services/index.js';
import { bindClient, type BoundClient } from './client-instance.js';
import type { FaaSClientOptions } from './http-transport.js';
import { bindExecutionHelpers } from './execution-client.js';
import type { ExecutionHelpers } from './executions.js';

/** Full service surface; preview operations still require server eligibility. */
export type GregaleClient = BoundClient<typeof services> & ExecutionHelpers;

export function createClient(baseURL: string, options: FaaSClientOptions = {}): GregaleClient {
  return bindClient(services, baseURL, options, bindExecutionHelpers);
}
