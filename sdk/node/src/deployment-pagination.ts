import { bindService, type ClientConfig } from './client-instance.js';
import type { DeploymentsService } from './generated/services/DeploymentsService.js';
import type { DeploymentResponse } from './generated/models/DeploymentResponse.js';
import type { DeploymentListResponse } from './generated/models/DeploymentListResponse.js';
import type { BuildResponse } from './generated/models/BuildResponse.js';
import type { BuildListResponse } from './generated/models/BuildListResponse.js';
import type { CancelablePromise } from './generated/core/CancelablePromise.js';
import type { RequestOptions } from './request-options.js';

/** Options for one lazy traversal. Limits count pages and items, not HTTP retry attempts. */
export interface PaginationOptions extends RequestOptions {
  /** Total traversal budget from the first next(), including consumer pauses.
   * Defaults to the client timeout or 30 seconds. Zero disables it. */
  readonly timeoutMs?: number;
  /** Maximum pages to fetch. Zero fetches nothing; omitted means no page cap. */
  readonly maxPages?: number;
  /** Maximum items to fetch. Remaining budget reduces the next page's limit.
   * Zero fetches nothing; omitted means no item cap. */
  readonly maxItems?: number;
}

/** Invalid server page or cursor cycle. Cursor values and bodies are excluded. */
export class PaginationError extends Error {
  override readonly name = 'PaginationError';
  constructor(readonly code: 'invalid_page' | 'repeated_cursor') {
    super(code === 'repeated_cursor' ? 'Pagination cursor repeated' : 'Invalid pagination response');
  }
}

type AppParams = Parameters<typeof DeploymentsService.listAppDeployments>[0];
type AccountParams = Parameters<typeof DeploymentsService.listDeployments>[0];
type BuildParams = Parameters<typeof DeploymentsService.getBuilds>[0];
type Service = Pick<typeof DeploymentsService, 'listAppDeployments' | 'listDeployments' | 'getBuilds'>;

/** Bound helpers available on public and preview client.deployments. */
export interface DeploymentPagination {
  iterateAppDeploymentPages(params: AppParams, options?: PaginationOptions): AsyncGenerator<DeploymentListResponse, void, unknown>;
  iterateAppDeployments(params: AppParams, options?: PaginationOptions): AsyncGenerator<DeploymentResponse, void, unknown>;
  iterateDeploymentPages(params?: AccountParams, options?: PaginationOptions): AsyncGenerator<DeploymentListResponse, void, unknown>;
  iterateDeployments(params?: AccountParams, options?: PaginationOptions): AsyncGenerator<DeploymentResponse, void, unknown>;
  iterateBuildPages(params?: BuildParams, options?: PaginationOptions): AsyncGenerator<BuildListResponse, void, unknown>;
  iterateBuilds(params?: BuildParams, options?: PaginationOptions): AsyncGenerator<BuildResponse, void, unknown>;
}

type CursorParams = { limit?: number; before?: string };
type CursorPage = { items: unknown[]; next_before?: string | null };
type PageRequest<Params, Page> = (params: Params, options?: RequestOptions) => CancelablePromise<Page>;

function limit(value: number | undefined, name: string): number {
  if (value === undefined) return Infinity;
  if (!Number.isSafeInteger(value) || value < 0) throw new TypeError(`${name} must be a nonnegative safe integer`);
  return value;
}

function paginate<Params extends CursorParams, Page extends CursorPage, Value>(
  fetchPage: PageRequest<Params, Page>, params: Params, options: PaginationOptions,
  clientTimeout: number | undefined, select: (page: Page) => Iterable<Value>,
): AsyncGenerator<Value, void, unknown> {
  const filters = { ...params };
  const policy = { ...options, retry: options.retry && { ...options.retry } };
  const stop = new AbortController();
  const signal = policy.signal ? AbortSignal.any([policy.signal, stop.signal]) : stop.signal;
  async function* traverse(): AsyncGenerator<Value, void, unknown> {
    const maxPages = limit(policy.maxPages, 'maxPages'), maxItems = limit(policy.maxItems, 'maxItems');
    const pageSize = filters.limit ?? 50;
    if (!Number.isInteger(pageSize) || pageSize < 1 || pageSize > 200) throw new TypeError('limit must be an integer from 1 through 200');
    if (filters.before !== undefined && typeof filters.before !== 'string') throw new TypeError('before must be a string');
    const budget = policy.timeoutMs ?? clientTimeout ?? 30_000;
    if (!Number.isInteger(budget) || budget < 0 || budget > 2 ** 31 - 1) throw new TypeError('timeoutMs must be an integer from 0 through 2147483647');
    const deadline = budget === 0 ? Infinity : performance.now() + budget;
    function remaining(): number {
      signal.throwIfAborted();
      if (deadline === Infinity) return 0;
      const left = deadline - performance.now();
      if (left <= 0) throw new DOMException('SDK pagination deadline exceeded', 'TimeoutError');
      return Math.ceil(left);
    }
    let before = filters.before, pages = 0, items = 0;
    const seen = new Set(before ? [before] : []);
    while (pages < maxPages && items < maxItems) {
      const requested = Math.min(pageSize, maxItems - items);
      const page = await fetchPage({ ...filters, before, limit: requested }, {
        signal, timeoutMs: remaining(), retry: policy.retry, onResponse: policy.onResponse,
      });
      remaining();
      if (!page || !Array.isArray(page.items) || page.items.length > requested ||
          (page.next_before != null && typeof page.next_before !== 'string')) throw new PaginationError('invalid_page');
      // Capture progress before yielding: consumer mutations cannot redirect the walk.
      const next = page.next_before || undefined;
      if (next && seen.has(next)) throw new PaginationError('repeated_cursor');
      if (next) seen.add(next);
      pages++; items += page.items.length;
      for (const value of select(page)) { remaining(); yield value; }
      if (!next) return;
      before = next;
    }
  }
  const iterator = traverse();
  // Native generator.return() queues behind a pending next(). Abort first so a
  // stalled fetch/body/backoff also retires promptly when the consumer closes it.
  return {
    next: (...args) => iterator.next(...args),
    return(value) { stop.abort(new DOMException('Pagination iterator closed', 'AbortError')); return iterator.return(value); },
    throw(reason) { stop.abort(reason); return iterator.throw(reason); },
    [Symbol.asyncIterator]() { return this; },
  };
}

/** Capture configuration and caller-owned inputs at invocation, before lazy next(). */
export function bindDeploymentPagination(service: Service, getConfig: () => ClientConfig): DeploymentPagination {
  function methods<Params extends CursorParams, Page extends CursorPage>(choose: (requests: ReturnType<typeof bindService<Service>>) => PageRequest<Params, Page>) {
    function start<Value>(params: Params, options: PaginationOptions = {}, select: (page: Page) => Iterable<Value>) {
      const config = getConfig();
      return paginate(choose(bindService(service, () => config)), params, options, config.timeoutMs, select);
    }
    return {
      pages: (params: Params, options?: PaginationOptions) => start(params, options, page => [page]),
      items: (params: Params, options?: PaginationOptions) => start<Page['items'][number]>(params, options, page => page.items as Page['items']),
    };
  }
  const app = methods<AppParams, DeploymentListResponse>(requests => requests.listAppDeployments);
  const account = methods<AccountParams, DeploymentListResponse>(requests => requests.listDeployments);
  const builds = methods<BuildParams, BuildListResponse>(requests => requests.getBuilds);
  return {
    iterateAppDeploymentPages: app.pages, iterateAppDeployments: app.items,
    iterateDeploymentPages: (params = {}, options) => account.pages(params, options),
    iterateDeployments: (params = {}, options) => account.items(params, options),
    iterateBuildPages: (params = {}, options) => builds.pages(params, options),
    iterateBuilds: (params = {}, options) => builds.items(params, options),
  };
}
