import * as services from './generated-public/services/index.js';
import { bindClient, type BoundClient } from './client-instance.js';
import type { FaaSClientOptions } from './http-transport.js';

export type GregaleClient = BoundClient<typeof services>;

/** Create an independent Node client. Never changes OpenAPI or global fetch.
 * Service methods live under namespaces: client.apps.getApp({ slug }).
 */
export function createClient(baseURL: string, options: FaaSClientOptions = {}): GregaleClient {
  return bindClient(services, baseURL, options);
}
