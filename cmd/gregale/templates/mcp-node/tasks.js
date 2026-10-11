import { setTimeout as delay } from 'node:timers/promises';
import { createHash, randomUUID } from 'node:crypto';
import * as z from 'zod/v4';

export const MCP_TASKS_EXTENSION_ID = 'io.modelcontextprotocol/tasks';
const LEASE_MS = 30_000;
const HEARTBEAT_MS = 5_000;
const WORKER_CAPABILITY_HEARTBEAT_MS = 10_000;
const MAX_ATTEMPTS = 3;
const MAX_TASK_SUBSCRIPTIONS = 64;
const MAX_TASK_SUBSCRIPTION_IDS = 64;
const MAX_WATCHED_TASKS = 512;
const TASK_INPUT_REQUIRED = Symbol('task input required');
const INPUT_METHODS = new Set(['elicitation/create', 'sampling/createMessage', 'roots/list']);
const TASK_ID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const TASK_TOOL_NAME_PATTERN = /^[\x21-\x7e]{1,128}$/;
const TASK_HANDLER_VERSION_PATTERN = /^[A-Za-z0-9._-]{1,64}$/;

export function clientSupportsMcpTasks(ctx) {
  const capabilities = ctx?.mcpReq?.envelope?.['io.modelcontextprotocol/clientCapabilities'];
  return !!capabilities?.extensions?.[MCP_TASKS_EXTENSION_ID];
}

function wireTask(task, pollIntervalMs) {
  const createdAt = new Date(task.created_at).toISOString();
  const lastUpdatedAt = new Date(task.updated_at).toISOString();
  const ttlMs = Math.max(0, new Date(task.expires_at).getTime() - new Date(task.created_at).getTime());
  const status = task.status === 'queued' || task.status === 'running' ? 'working' : task.status;
  const result = {
    resultType: 'complete', taskId: task.task_id, status,
    createdAt, lastUpdatedAt, ttlMs, pollIntervalMs,
  };
  if (status === 'completed') result.result = task.result;
  if (status === 'failed') result.error = task.error || { code: -32603, message: 'Task execution failed' };
  if (status === 'input_required') result.inputRequests = task.input_requests || {};
  if (task.statusMessage) result.statusMessage = task.statusMessage;
  return result;
}

function validateInputRequests(requests) {
  if (!requests || typeof requests !== 'object' || Array.isArray(requests)) throw new Error('MCP task input requests must be an object');
  const entries = Object.entries(requests);
  if (entries.length < 1 || entries.length > 64) throw new Error('MCP task input request batches must contain between 1 and 64 requests');
  for (const [key, request] of entries) {
    if (!key || key.length > 128) throw new Error('MCP task input request keys must contain between 1 and 128 characters');
    if (!request || typeof request !== 'object' || Array.isArray(request) || !INPUT_METHODS.has(request.method)) {
      throw new Error('MCP task input requests must use a supported server-to-client MCP method');
    }
    if (!request.params || typeof request.params !== 'object' || Array.isArray(request.params)) {
      throw new Error('MCP task input request params must be an object');
    }
    if (request.method === 'elicitation/create' && !['form', 'url'].includes(request.params.mode)) {
      throw new Error('MCP elicitation task requests must declare form or url mode');
    }
  }
}

function inputMethodsFor(clientCapabilities) {
  const methods = [];
  const elicitation = clientCapabilities?.elicitation;
  if (elicitation && typeof elicitation === 'object') {
    if (elicitation.form || Object.keys(elicitation).length === 0) methods.push('elicitation/create:form');
    if (elicitation.url) methods.push('elicitation/create:url');
  }
  if (clientCapabilities?.sampling && typeof clientCapabilities.sampling === 'object') methods.push('sampling/createMessage');
  if (clientCapabilities?.roots && typeof clientCapabilities.roots === 'object') methods.push('roots/list');
  return methods;
}

export function createMcpTaskRuntime({ store, handlers, deploymentID = '', pollIntervalMs = 2000, workerConcurrency = 1, workerEnabled = true, keepAlive = false, drainTimeoutMs = 10_000, onDrain = () => {}, onError = () => {} }) {
  if (!store || typeof store.initialize !== 'function' || typeof store.claim !== 'function') throw new Error('MCP Tasks require a durable task store');
  if (!handlers || typeof handlers !== 'object' || Array.isArray(handlers)) throw new Error('MCP Tasks require a handler registry');
  if (!Number.isSafeInteger(pollIntervalMs) || pollIntervalMs < 500 || pollIntervalMs > 30_000) throw new Error('MCP task polling must be between 500 and 30000 milliseconds');
  if (!Number.isSafeInteger(workerConcurrency) || workerConcurrency < 1 || workerConcurrency > 16) throw new Error('MCP task worker concurrency must be between 1 and 16');
  if (typeof workerEnabled !== 'boolean' || typeof keepAlive !== 'boolean') throw new Error('MCP task worker options must be boolean');

  if (!Number.isSafeInteger(drainTimeoutMs) || drainTimeoutMs < 1000 || drainTimeoutMs > 300_000) throw new Error('MCP drain timeout must be between 1 and 300 seconds');
  const supportedHandlers = [];
  const executionHandlers = new Map();
  for (const [name, handler] of Object.entries(handlers)) {
    if (!TASK_TOOL_NAME_PATTERN.test(name) || !handler || typeof handler.version !== 'string' || !TASK_HANDLER_VERSION_PATTERN.test(handler.version)) throw new Error('Invalid MCP task handler registration');
    if (handler.validate !== undefined && typeof handler.validate !== 'function') throw new Error('MCP task handler validate must be a function');
    for (const [version, execute] of Object.entries({ ...handler.previousVersions, [handler.version]: handler.execute })) {
      if (!TASK_HANDLER_VERSION_PATTERN.test(version) || typeof execute !== 'function') throw new Error('Invalid MCP task handler registry');
      supportedHandlers.push({ name, version });
      executionHandlers.set(JSON.stringify([name, version]), execute);
    }
  }

  const active = new Set();
  const executions = new Map();
  let stopPromise;
  let claimPromise;
  let capabilityPromise;
  const taskSubscriptions = new Set();
  let timer;
  let workerCapabilityTimer;
  let workerCapabilityHeartbeatBusy = false;
  let lastWorkerCapabilityCleanup = 0;
  const workerID = randomUUID();
  let taskSubscriptionTimer;
  let taskSubscriptionPollBusy = false;
  let taskStoreUnsubscribe;
  let taskStoreSubscribePromise;
  const pendingTaskChanges = new Set();
  let draining = false;
  let closed = true;
  let lastCleanup = 0;

  function reportError() {
    try { onError(); } catch { /* Never let diagnostics stop task workers. */ }
  }

  async function refreshWorkerCapabilities() {
    if (!workerEnabled || closed || workerCapabilityHeartbeatBusy || typeof store.heartbeatWorkerCapabilities !== 'function') return;
    workerCapabilityHeartbeatBusy = true;
    try {
      capabilityPromise = store.heartbeatWorkerCapabilities(workerID, supportedHandlers, deploymentID);
      await capabilityPromise;
      const now = Date.now();
      if (now - lastWorkerCapabilityCleanup >= 60 * 60 * 1000 && typeof store.cleanupStaleWorkerCapabilities === 'function') {
        await store.cleanupStaleWorkerCapabilities();
        lastWorkerCapabilityCleanup = now;
      }
    } catch {
      reportError();
    } finally {
      workerCapabilityHeartbeatBusy = false;
    }
  }

  function ownerPartition(authInfo, authMode) {
    if (authMode === 'open') return 'open';
    const resource = authInfo?.resource instanceof URL ? authInfo.resource.href : String(authInfo?.resource || '');
    const clientId = typeof authInfo?.clientId === 'string' ? authInfo.clientId : '';
    const principal = JSON.stringify([resource, authInfo?.extra?.subject, clientId]);
    return createHash('sha256').update(principal).digest('hex');
  }

  function detailTask(task) {
    const { resultType: _resultType, ...detail } = wireTask(task, pollIntervalMs);
    return detail;
  }

  async function taskSnapshots(watchers) {
    const groups = new Map();
    for (const watcher of watchers) {
      const key = ownerPartition(watcher.authInfo, watcher.authMode);
      let group = groups.get(key);
      if (!group) {
        group = { authInfo: watcher.authInfo, authMode: watcher.authMode, taskIDs: new Set(), watchers: [] };
        groups.set(key, group);
      }
      group.watchers.push(watcher);
      for (const taskID of watcher.taskIDs) group.taskIDs.add(taskID);
    }

    const snapshots = new Map();
    for (const [ownerKey, group] of groups) {
      try {
        let tasks;
        if (typeof store.getMany === 'function') {
          tasks = await store.getMany({ taskIDs: [...group.taskIDs], authInfo: group.authInfo, authMode: group.authMode });
        } else {
          tasks = await Promise.all([...group.taskIDs].map(taskID => store.get({ taskID, authInfo: group.authInfo, authMode: group.authMode })));
        }
        snapshots.set(ownerKey, new Map(tasks.filter(Boolean).map(task => [task.task_id, task])));
      } catch {
        reportError();
        snapshots.set(ownerKey, null);
      }
    }
    return snapshots;
  }

  function removeTaskSubscription(watcher) {
    if (!taskSubscriptions.delete(watcher)) return;
    if (taskSubscriptions.size === 0) {
      clearInterval(taskSubscriptionTimer);
      taskSubscriptionTimer = undefined;
      const unsubscribe = taskStoreUnsubscribe;
      taskStoreUnsubscribe = undefined;
      taskStoreSubscribePromise = undefined;
      if (unsubscribe) void unsubscribe();
    }
  }

  async function pollTaskSubscriptions(changedTaskID) {
    if (changedTaskID) pendingTaskChanges.add(changedTaskID);
    if (closed || taskSubscriptionPollBusy || taskSubscriptions.size === 0) return;
    taskSubscriptionPollBusy = true;
    try {
      do {
        const changed = pendingTaskChanges.size > 0 ? new Set(pendingTaskChanges) : null;
        pendingTaskChanges.clear();
        const watchers = [...taskSubscriptions].filter(watcher => watcher.ready && !watcher.closed
          && (!changed || [...watcher.taskIDs].some(taskID => changed.has(taskID))));
        const snapshots = await taskSnapshots(watchers);
        for (const watcher of watchers) {
          const current = snapshots.get(watcher.ownerKey);
          if (!current) continue;
          for (const taskID of [...watcher.taskIDs]) {
            if (changed && !changed.has(taskID)) continue;
            const task = current.get(taskID);
            if (!task || (watcher.canAccessTool && !watcher.canAccessTool(task.tool_name))) {
              watcher.taskIDs.delete(taskID);
              watcher.last.delete(taskID);
              continue;
            }
            const detail = detailTask(task);
            const fingerprint = JSON.stringify(detail);
            if (watcher.last.get(taskID) === fingerprint) continue;
            watcher.last.set(taskID, fingerprint);
            try { watcher.onTask(detail); } catch { reportError(); }
          }
        }
      } while (pendingTaskChanges.size > 0 && !closed);
      if (![...taskSubscriptions].some(watcher => watcher.taskIDs.size > 0)) {
        clearInterval(taskSubscriptionTimer);
        taskSubscriptionTimer = undefined;
      }
    } finally {
      taskSubscriptionPollBusy = false;
    }
  }

  function startTaskSubscriptionPolling() {
    if (!taskSubscriptionTimer && !closed) {
      const fallbackMs = typeof store.subscribe === 'function' ? Math.max(5000, pollIntervalMs) : Math.max(1000, pollIntervalMs);
      taskSubscriptionTimer = setInterval(() => { void pollTaskSubscriptions(); }, fallbackMs);
      taskSubscriptionTimer.unref?.();
    }
    if (typeof store.subscribe === 'function' && !taskStoreUnsubscribe && !taskStoreSubscribePromise) {
      const starting = Promise.resolve(store.subscribe(event => {
        if (event?.taskID) void pollTaskSubscriptions(event.taskID);
      })).then(unsubscribe => {
        if (closed || taskSubscriptions.size === 0) void unsubscribe();
        else taskStoreUnsubscribe = unsubscribe;
      });
      taskStoreSubscribePromise = starting.catch(() => { reportError(); });
      const current = taskStoreSubscribePromise;
      void current.then(() => {
        if (taskStoreSubscribePromise === current) taskStoreSubscribePromise = undefined;
      });
    }
    return taskStoreSubscribePromise;
  }

  async function runTask(task) {
    const execute = executionHandlers.get(JSON.stringify([task.tool_name, task.handler_version]));
    if (!execute) {
      await store.fail(task.task_id, task.lease_token, { code: -32603, message: 'Task handler is unavailable' });
      return;
    }

    const controller = new AbortController();
    let abortReason = '';
    executions.set(task.task_id, { task, abort() { abortReason = 'drain_timeout'; controller.abort(); } });
    let heartbeatBusy = false;
    let pausedForInput = false;
    const heartbeat = setInterval(async () => {
      if (heartbeatBusy || controller.signal.aborted || pausedForInput) return;
      heartbeatBusy = true;
      try {
        const state = await store.heartbeat(task.task_id, task.lease_token, LEASE_MS);
        if (!state.owned) {
          abortReason = 'lease_lost';
          controller.abort();
        } else if (state.cancelRequested) {
          abortReason = 'cancelled';
          controller.abort();
        }
      } catch {
        // A transient database error should not immediately interrupt useful work.
        reportError();
      } finally {
        heartbeatBusy = false;
      }
    }, HEARTBEAT_MS);
    heartbeat.unref?.();

    try {
      const requestInputs = async requests => {
        validateInputRequests(requests);
        for (const request of Object.values(requests)) {
          const capability = request.method === 'elicitation/create' ? `${request.method}:${request.params.mode}` : request.method;
          if (!task.input_methods?.includes(capability)) throw new Error(`MCP task client did not declare support for ${request.method}`);
        }
        const state = await store.requestInputs({ taskID: task.task_id, leaseToken: task.lease_token, requests });
        if (state?.cancelled) {
          await store.finishCancelled(task.task_id, task.lease_token);
          throw TASK_INPUT_REQUIRED;
        }
        if (!state?.ready) {
          pausedForInput = true;
          throw TASK_INPUT_REQUIRED;
        }
        return state.responses;
      };
      const result = await execute(task.arguments, {
        taskId: task.task_id,
        signal: controller.signal,
        requestInputs,
        async requestInput(key, request) {
          const responses = await requestInputs({ [key]: request });
          return responses[key];
        },
      });
      if (abortReason === 'cancelled') await store.finishCancelled(task.task_id, task.lease_token);
      else if (abortReason !== 'lease_lost' && abortReason !== 'drain_timeout') await store.complete(task.task_id, task.lease_token, result);
    } catch (error) {
      if (error === TASK_INPUT_REQUIRED) return;
      if (abortReason === 'cancelled') await store.finishCancelled(task.task_id, task.lease_token);
      else if (abortReason !== 'lease_lost' && abortReason !== 'drain_timeout') {
        await store.fail(task.task_id, task.lease_token, { code: -32603, message: 'Task execution failed' });
      }
    } finally {
      clearInterval(heartbeat);
      executions.delete(task.task_id);
    }
  }

  async function drain() {
    if (closed || draining) return;
    draining = true;
    try {
      const now = Date.now();
      if (now - lastCleanup >= 60_000 && typeof store.cleanupExpired === 'function') {
        lastCleanup = now;
        await store.cleanupExpired();
      }
      while (!closed && active.size < workerConcurrency) {
        claimPromise = store.claim(MAX_ATTEMPTS, LEASE_MS, supportedHandlers);
        const task = await claimPromise;
        if (!task) break;
        if (closed) { await store.release(task.task_id, task.lease_token); break; }
        let work;
        work = runTask(task).catch(reportError).finally(() => {
          active.delete(work);
          void drain();
        });
        active.add(work);
      }
    } catch {
      reportError();
    } finally {
      draining = false;
    }
  }

  return {
    async start() {
      if (!closed) return;
      await store.initialize();
      closed = false;
      if (workerEnabled) {
        await refreshWorkerCapabilities();
        workerCapabilityTimer = setInterval(() => { void refreshWorkerCapabilities(); }, WORKER_CAPABILITY_HEARTBEAT_MS);
        if (!keepAlive) workerCapabilityTimer.unref?.();
        timer = setInterval(() => { void drain(); }, pollIntervalMs);
        if (!keepAlive) timer.unref?.();
        await drain();
      }
    },
    async stop() {
      if (stopPromise) return stopPromise;
      if (closed) return;
      closed = true;
      clearInterval(timer);
      clearInterval(workerCapabilityTimer);
      const deadline = Date.now() + drainTimeoutMs;
      const emit = (state, unfinishedTasks = 0) => { try { onDrain({ state, unfinishedTasks, activeTasks: executions.size, deadline: new Date(deadline).toISOString() }); } catch { reportError(); } };
      emit('draining');
      let expired = [];
      let timeout;
      const expiry = new Promise(resolve => { timeout = setTimeout(() => {
        expired = [...executions.values()];
        for (const execution of expired) execution.abort();
        resolve();
      }, drainTimeoutMs); });
      stopPromise = (async () => {
        // Complete an in-flight registration before withdrawing capabilities.
        await capabilityPromise?.catch(reportError);
        if (workerEnabled && typeof store.removeWorkerCapabilities === 'function') {
          try { await store.removeWorkerCapabilities(workerID); } catch { reportError(); }
        }
        clearInterval(taskSubscriptionTimer);
        taskSubscriptionTimer = undefined;
        const unsubscribe = taskStoreUnsubscribe;
        taskStoreUnsubscribe = undefined;
        taskStoreSubscribePromise = undefined;
        if (unsubscribe) await unsubscribe();
        for (const watcher of [...taskSubscriptions]) {
          watcher.closed = true;
          try { watcher.onClose?.(); } catch { reportError(); }
          taskSubscriptions.delete(watcher);
        }
        // Claims that finish after shutdown are returned without execution.
        await claimPromise?.catch(reportError);
        await Promise.race([Promise.allSettled([...active]), expiry]);
        clearTimeout(timeout);
        const unfinished = [...new Map([...expired, ...executions.values()].map(execution => [execution.task.task_id, execution])).values()];
        // Abort locally before revoking leases: even an uncooperative handler
        // returning during release cannot publish its result.
        for (const execution of unfinished) execution.abort();
        try {
          if (typeof store.releaseMany === 'function') await store.releaseMany(unfinished.map(({ task }) => task));
          else await Promise.all(unfinished.map(({ task }) => store.release(task.task_id, task.lease_token)));
        } catch { reportError(); } // Lease expiry remains the fallback on database failure.
        emit(unfinished.length ? 'timed_out' : 'completed', unfinished.length);
      })();
      return stopPromise;
    },
    async create(toolName, args, authInfo, authMode, clientCapabilities = {}) {
      if (closed) throw new Error('MCP task runtime is draining');
      const handler = Object.hasOwn(handlers, toolName) ? handlers[toolName] : undefined;
      if (!handler || typeof handler.execute !== 'function') throw new Error('MCP task tool is not registered');
      handler.validate?.(args);
      const task = await store.create({ toolName, handlerVersion: handler.version, args, authInfo, authMode, inputMethods: inputMethodsFor(clientCapabilities) });
      if (workerEnabled) void drain();
      return {
        resultType: 'task', taskId: task.task_id, status: 'working',
        statusMessage: 'Task accepted for processing.',
        createdAt: new Date(task.created_at).toISOString(),
        lastUpdatedAt: new Date(task.updated_at).toISOString(),
        ttlMs: Math.max(0, new Date(task.expires_at).getTime() - new Date(task.created_at).getTime()),
        pollIntervalMs,
      };
    },
    canCreate(toolName) {
      const handler = Object.hasOwn(handlers, toolName) ? handlers[toolName] : undefined;
      return !!handler && typeof handler.execute === 'function';
    },
    async get(taskID, authInfo, authMode, canAccessTool) {
      const task = await store.get({ taskID, authInfo, authMode });
      if (!task || (canAccessTool && !canAccessTool(task.tool_name))) throw new Error('Task not found');
      return wireTask(task, pollIntervalMs);
    },
    async cancel(taskID, authInfo, authMode, canAccessTool) {
      const task = await store.get({ taskID, authInfo, authMode });
      if (!task || (canAccessTool && !canAccessTool(task.tool_name))) throw new Error('Task not found');
      if (!await store.requestCancel({ taskID, authInfo, authMode })) throw new Error('Task not found');
      return { resultType: 'complete' };
    },
    async update(taskID, inputResponses, authInfo, authMode, canAccessTool) {
      const task = await store.get({ taskID, authInfo, authMode });
      if (!task || (canAccessTool && !canAccessTool(task.tool_name))) throw new Error('Task not found');
      if (!inputResponses || typeof inputResponses !== 'object' || Array.isArray(inputResponses)) throw new Error('Task input responses must be an object');
      if (!await store.updateInputs({ taskID, authInfo, authMode, inputResponses })) throw new Error('Task not found');
      if (workerEnabled) void drain();
      return { resultType: 'complete' };
    },
    async subscribe(taskIDs, authInfo, authMode, canAccessTool, onTask, onClose) {
      if (closed) throw new Error('MCP task runtime is not started');
      if (!Array.isArray(taskIDs) || taskIDs.length > MAX_TASK_SUBSCRIPTION_IDS || taskIDs.some(taskID => typeof taskID !== 'string' || !TASK_ID_PATTERN.test(taskID))) {
        throw new Error('MCP task subscriptions require at most 64 valid task IDs');
      }
      if (typeof onTask !== 'function') throw new Error('MCP task subscriptions require an update handler');
      if (taskSubscriptions.size >= MAX_TASK_SUBSCRIPTIONS) throw new Error('MCP task subscription capacity reached');
      const ids = [...new Set(taskIDs)];
      const watchedCount = [...taskSubscriptions].reduce((count, watcher) => count + watcher.taskIDs.size, 0);
      if (watchedCount + ids.length > MAX_WATCHED_TASKS) throw new Error('MCP task subscription capacity reached');

      const watcher = {
        taskIDs: new Set(ids), authInfo, authMode, canAccessTool, onTask, onClose,
        ownerKey: ownerPartition(authInfo, authMode), last: new Map(), ready: false, closed: false,
      };
      taskSubscriptions.add(watcher);
      try {
        if (ids.length > 0) await startTaskSubscriptionPolling();
        const snapshots = await taskSnapshots([watcher]);
        const tasksByID = snapshots.get(watcher.ownerKey);
        if (!tasksByID) throw new Error('Could not read MCP task subscription state');
        const tasks = [];
        for (const taskID of ids) {
          const task = tasksByID.get(taskID);
          if (!task || (canAccessTool && !canAccessTool(task.tool_name))) {
            watcher.taskIDs.delete(taskID);
            continue;
          }
          const detail = detailTask(task);
          watcher.last.set(taskID, JSON.stringify(detail));
          tasks.push(detail);
        }
        watcher.ready = true;
        if (watcher.taskIDs.size > 0) startTaskSubscriptionPolling();
        else {
          watcher.closed = true;
          removeTaskSubscription(watcher);
        }
        return {
          taskIds: [...watcher.taskIDs],
          tasks,
          close: () => {
            if (watcher.closed) return;
            watcher.closed = true;
            removeTaskSubscription(watcher);
          },
        };
      } catch (error) {
        watcher.closed = true;
        removeTaskSubscription(watcher);
        throw error;
      }
    },
  };
}

export function installMcpTaskHandlers(server, runtime, authInfo, authMode, canAccessTool) {
  if (typeof canAccessTool !== 'function') throw new Error('MCP task handlers require the tool authorization policy');
  server.server.setRequestHandler('tasks/get', {
    params: z.object({ taskId: z.string().uuid() }),
  }, ({ taskId }) => runtime.get(taskId, authInfo, authMode, canAccessTool));
  server.server.setRequestHandler('tasks/cancel', {
    params: z.object({ taskId: z.string().uuid() }),
  }, ({ taskId }) => runtime.cancel(taskId, authInfo, authMode, canAccessTool));
  server.server.setRequestHandler('tasks/update', {
    params: z.object({ taskId: z.string().uuid() }),
  }, ({ taskId }, ctx) => runtime.update(taskId, ctx.mcpReq.inputResponses, authInfo, authMode, canAccessTool));
}

const registeredMcpTaskHandlers = Object.create(null);
export const mcpTaskHandlers = new Proxy(registeredMcpTaskHandlers, {
  set() { throw new TypeError('Register task handlers with registerMcpTaskHandler()'); },
  defineProperty() { throw new TypeError('Register task handlers with registerMcpTaskHandler()'); },
  deleteProperty() { throw new TypeError('Task handlers cannot be removed while the runtime is configured'); },
});

// Register handlers before starting the web or worker runtime. Keep the active
// version stable across all processes sharing a queue and retain old versions
// until their queued tasks have drained.
export function registerMcpTaskHandler(name, handler) {
  if (typeof name !== 'string' || !TASK_TOOL_NAME_PATTERN.test(name)) throw new TypeError('MCP task tool name must contain 1 to 128 visible ASCII characters');
  if (!handler || typeof handler !== 'object' || Array.isArray(handler)
    || typeof handler.version !== 'string' || !TASK_HANDLER_VERSION_PATTERN.test(handler.version)
    || typeof handler.validate !== 'function' || typeof handler.execute !== 'function') {
    throw new TypeError('MCP task handlers require a version, validate function, and execute function');
  }
  const previousVersions = handler.previousVersions ?? {};
  if (!previousVersions || typeof previousVersions !== 'object' || Array.isArray(previousVersions)) throw new TypeError('MCP task previousVersions must be an object');
  for (const [version, execute] of Object.entries(previousVersions)) {
    if (!TASK_HANDLER_VERSION_PATTERN.test(version) || version === handler.version || typeof execute !== 'function') {
      throw new TypeError('Invalid MCP task previous handler version');
    }
  }
  if (Object.hasOwn(registeredMcpTaskHandlers, name)) throw new Error(`MCP task handler ${name} is already registered`);
  registeredMcpTaskHandlers[name] = Object.freeze({
    version: handler.version,
    previousVersions: Object.freeze({ ...previousVersions }),
    validate: handler.validate,
    execute: handler.execute,
  });
  return mcpTaskHandlers[name];
}

registerMcpTaskHandler('build_report', {
    version: '2',
    previousVersions: {
      '1': async ({ report, steps }, { signal }) => {
        for (let step = 0; step < steps; step++) await delay(200, undefined, { signal });
        return { content: [{ type: 'text', text: `Report ${report} is ready after ${steps} steps.` }] };
      },
    },
    validate(args) {
      if (!args || typeof args !== 'object' || Array.isArray(args)
        || typeof args.report !== 'string' || !args.report.trim() || args.report.length > 80
        || !Number.isInteger(args.steps) || args.steps < 1 || args.steps > 20
        || (args.request_approval !== undefined && typeof args.request_approval !== 'boolean')) {
        throw new Error('Invalid build_report task arguments');
      }
    },
    async execute({ report, steps, request_approval }, { signal, requestInput }) {
      if (request_approval) {
        const response = await requestInput('approval', {
          method: 'elicitation/create',
          params: {
            mode: 'form',
            message: 'Approve generating this report?',
            requestedSchema: {
              type: 'object',
              properties: { approved: { type: 'boolean', title: 'Approve report generation' } },
              required: ['approved'],
            },
          },
        });
        if (response.action !== 'accept' || response.content?.approved !== true) {
          return { content: [{ type: 'text', text: 'Report was not approved.' }] };
        }
      }
      for (let step = 0; step < steps; step++) await delay(200, undefined, { signal });
      return { content: [{ type: 'text', text: `Report ${report} is ready after ${steps} steps.` }] };
    },
});
