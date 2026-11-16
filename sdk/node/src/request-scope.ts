import { CancelablePromise } from './generated/core/CancelablePromise.js';
import type { ApiRequestOptions } from './generated/core/ApiRequestOptions.js';
import { request } from './generated/core/request.js';
import type { ClientConfig } from './client-instance.js';
import type { RequestOptions } from './request-options.js';

/** One scope owns the deadline, caller listener and generated request. The
 * generated core delegates here only for isolated bound calls. Legacy static
 * services keep their original request path.
 */
export function scopedRequest<T>(config: ClientConfig, api: ApiRequestOptions, options: RequestOptions = {}): CancelablePromise<T> {
  return new CancelablePromise<T>((resolve, reject, onCancel) => {
    const controller = new AbortController();
    let pending: CancelablePromise<T> | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const caller = options.signal;
    const onCallerAbort = () => controller.abort(caller?.reason);
    const cleanup = () => {
      clearTimeout(timer);
      caller?.removeEventListener('abort', onCallerAbort);
      controller.signal.removeEventListener('abort', onAbort);
    };
    const onAbort = () => {
      pending?.cancel();
      cleanup();
      reject(controller.signal.reason);
    };
    onCancel(() => { controller.abort(); pending?.cancel(); cleanup(); });
    controller.signal.addEventListener('abort', onAbort, { once: true });
    try {
      if (caller?.aborted) { onCallerAbort(); return; }
      caller?.addEventListener('abort', onCallerAbort, { once: true });
      const budget = options.timeoutMs ?? config.timeoutMs ??
        (String(api.headers?.Accept ?? '').includes('text/event-stream') ? 0 : 30_000);
      if (!Number.isInteger(budget) || budget < 0 || budget > 2 ** 31 - 1) {
        throw new TypeError('timeoutMs must be an integer from 0 through 2147483647');
      }
      if (budget > 0) timer = setTimeout(() => {
        controller.abort(new DOMException('SDK request deadline exceeded', 'TimeoutError'));
      }, budget);
      const fetch = config.requestFetch({ ...options, signal: controller.signal, timeoutMs: 0 });
      pending = request<T>({
        ...config, REQUEST: undefined,
        FETCH: (input, init) => fetch(input, {
          ...init,
          signal: init?.signal ? AbortSignal.any([init.signal, controller.signal]) : controller.signal,
        }),
      }, api);
      pending.then(value => { cleanup(); resolve(value); }, error => {
        cleanup(); reject(controller.signal.aborted ? controller.signal.reason : error);
      });
    } catch (error) {
      cleanup(); reject(error);
    }
  });
}
