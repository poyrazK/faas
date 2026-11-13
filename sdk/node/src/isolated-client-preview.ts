import * as services from './generated/services/index.js';
import { bindClient, type BoundClient } from './client-instance.js';
import type { FaaSClientOptions } from './http-transport.js';

/** Full service surface; preview operations still require server eligibility. */
export type GregaleClient = BoundClient<typeof services>;

export function createClient(baseURL: string, options: FaaSClientOptions = {}): GregaleClient {
  return bindClient(services, baseURL, options);
}
