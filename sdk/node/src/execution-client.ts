import { bindService, type ClientConfig } from './client-instance.js';
import { getHeaders } from './generated/core/request.js';
import { RunsService } from './generated/services/RunsService.js';
import {
  runExecutionWithClient,
  watchExecutionWithClient,
  type ExecutionHelpers,
  type ExecutionRequests,
} from './executions.js';

/** Preview-only helpers. Capture once at invocation, including before a lazy
 * iterator's first next(), so rotations cannot redirect an active workflow.
 */
export function bindExecutionHelpers(getConfig: () => ClientConfig): ExecutionHelpers {
  function snapshot(): ExecutionRequests {
    const config = getConfig();
    return {
      runs: bindService(RunsService, () => config),
      async fetchEvents(path, signal) {
        const headers = await getHeaders(config, {
          method: 'GET', url: path, headers: { Accept: 'text/event-stream' },
        });
        return config.FETCH(`${config.BASE}${path}`, { method: 'GET', headers, signal });
      },
    };
  }
  return {
    watchExecution(id, options = {}) {
      return watchExecutionWithClient(snapshot(), id, options);
    },
    runExecution(requestBody, options = {}) {
      return runExecutionWithClient(snapshot(), requestBody, options);
    },
  };
}
