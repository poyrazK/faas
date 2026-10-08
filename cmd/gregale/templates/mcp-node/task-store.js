import { createCipheriv, createDecipheriv, createHash, createHmac, randomBytes, randomUUID } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';
import { isDeepStrictEqual } from 'node:util';
import defaults from './task-limits.json' with { type: 'json' };

const TABLE = 'gregale_mcp_tasks';
const FAIRNESS_TABLE = 'gregale_mcp_task_fairness';
const FAIRNESS_SEQUENCE = 'gregale_mcp_task_claim_order_seq';
const FAIRNESS_CLAIM_RETRIES = 16;
const MAX_ARGUMENT_BYTES = 256 * 1024;
const MAX_RESULT_BYTES = 1024 * 1024;
const MAX_INPUT_STATE_BYTES = 256 * 1024;
const SUPPORTED_INPUT_METHODS = new Set(['elicitation/create:form', 'elicitation/create:url', 'sampling/createMessage', 'roots/list']);
const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

const CREATE_SCHEMA = `
  CREATE TABLE IF NOT EXISTS ${TABLE} (
    namespace text NOT NULL,
    task_id uuid NOT NULL,
    owner_hash bytea NOT NULL,
    tool_name text NOT NULL,
    handler_version text NOT NULL,
    arguments_encrypted bytea NOT NULL,
    status text NOT NULL CHECK (status IN ('queued', 'running', 'input_required', 'completed', 'cancelled', 'failed')),
    result_encrypted bytea,
    error_encrypted bytea,
    input_state_encrypted bytea,
    input_methods text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    resume_pending boolean NOT NULL DEFAULT false,
    next_attempt_at timestamptz,
    lease_token uuid,
    lease_expires_at timestamptz,
    cancel_requested_at timestamptz,
    PRIMARY KEY (namespace, task_id),
    CHECK ((status = 'running') = (lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)),
    CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL))
  )`;

const MIGRATE_SCHEMA = `
  ALTER TABLE ${TABLE}
    ADD COLUMN IF NOT EXISTS input_state_encrypted bytea,
    ADD COLUMN IF NOT EXISTS input_methods text[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS resume_pending boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz;
  ALTER TABLE ${TABLE} DROP CONSTRAINT IF EXISTS ${TABLE}_status_check;
  ALTER TABLE ${TABLE} ADD CONSTRAINT ${TABLE}_status_check
    CHECK (status IN ('queued', 'running', 'input_required', 'completed', 'cancelled', 'failed'));
  ALTER TABLE ${TABLE} DROP CONSTRAINT IF EXISTS ${TABLE}_input_state_check;
  ALTER TABLE ${TABLE} ADD CONSTRAINT ${TABLE}_input_state_check
    CHECK (status <> 'input_required' OR input_state_encrypted IS NOT NULL);
`;

const CREATE_QUEUE_INDEX = `
  CREATE INDEX IF NOT EXISTS gregale_mcp_tasks_queue_idx
    ON ${TABLE} (namespace, created_at, task_id)
    WHERE status IN ('queued', 'running')`;

const CREATE_ADMISSION_INDEX = `
  CREATE INDEX IF NOT EXISTS gregale_mcp_tasks_admission_idx
    ON ${TABLE} (namespace, owner_hash, expires_at)
    WHERE status IN ('queued', 'running', 'input_required')`;

const CREATE_FAIRNESS_SCHEMA = `
  CREATE TABLE IF NOT EXISTS ${FAIRNESS_TABLE} (
    namespace text NOT NULL,
    owner_hash bytea NOT NULL,
    last_claimed_order bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (namespace, owner_hash)
  )`;

const CREATE_FAIRNESS_SEQUENCE = `CREATE SEQUENCE IF NOT EXISTS ${FAIRNESS_SEQUENCE} AS bigint`;

const CREATE_FAIRNESS_INDEX = `
  CREATE INDEX IF NOT EXISTS gregale_mcp_task_fairness_order_idx
    ON ${FAIRNESS_TABLE} (namespace, last_claimed_order, owner_hash)`;

const CREATE_TASK_FAIRNESS_FUNCTION = `
  CREATE OR REPLACE FUNCTION gregale_mcp_task_seed_fairness() RETURNS trigger
  LANGUAGE plpgsql AS $$
  BEGIN
    INSERT INTO ${FAIRNESS_TABLE} (namespace, owner_hash)
    VALUES (NEW.namespace, NEW.owner_hash)
    ON CONFLICT (namespace, owner_hash) DO NOTHING;
    RETURN NEW;
  END;
  $$`;

const CREATE_TASK_FAIRNESS_TRIGGER = `
  DO $body$
  BEGIN
    IF NOT EXISTS (
      SELECT 1 FROM pg_trigger
       WHERE tgrelid = '${TABLE}'::regclass
         AND tgname = 'gregale_mcp_tasks_fairness_seed'
         AND NOT tgisinternal
    ) THEN
      CREATE TRIGGER gregale_mcp_tasks_fairness_seed
        AFTER INSERT ON ${TABLE}
        FOR EACH ROW EXECUTE FUNCTION gregale_mcp_task_seed_fairness();
    END IF;
  END;
  $body$`;

const CLAIMABLE_TASK_FILTER = `
  task.namespace = $1
  AND task.expires_at > clock_timestamp()
  AND (task.attempt_count < $2 OR (task.status = 'queued' AND task.resume_pending AND task.attempt_count = $2))
  AND ($5::jsonb IS NULL OR EXISTS (
    SELECT 1 FROM jsonb_to_recordset($5::jsonb) AS handler(name text, version text)
     WHERE handler.name = task.tool_name AND handler.version = task.handler_version
  ))
  AND ((task.status = 'queued' AND (task.next_attempt_at IS NULL OR task.next_attempt_at <= clock_timestamp())) OR (task.status = 'running' AND task.lease_expires_at <= clock_timestamp()))`;

// Claims serialize per namespace; this statement runs after acquiring the lock,
// so its snapshot includes every preceding committed lease.
const RUNNING_CAPACITY_FILTER = `
  (SELECT COUNT(*) FROM ${TABLE} AS live
    WHERE live.namespace = $1 AND live.status = 'running'
      AND live.expires_at > clock_timestamp() AND live.lease_expires_at > clock_timestamp()) < $6
  AND (SELECT COUNT(*) FROM ${TABLE} AS live
    WHERE live.namespace = $1 AND live.owner_hash = fairness.owner_hash AND live.status = 'running'
      AND live.expires_at > clock_timestamp() AND live.lease_expires_at > clock_timestamp()) < $7`;

const CREATE_TASK_NOTIFY_FUNCTION = `
  CREATE OR REPLACE FUNCTION gregale_mcp_task_notify_change() RETURNS trigger
  LANGUAGE plpgsql AS $$
  BEGIN
    IF TG_OP = 'INSERT' THEN
      PERFORM pg_notify('gregale_mcp_task_' || md5(NEW.namespace), NEW.task_id::text);
    ELSIF NEW.status IS DISTINCT FROM OLD.status OR
          NEW.updated_at IS DISTINCT FROM OLD.updated_at OR
          NEW.result_encrypted IS DISTINCT FROM OLD.result_encrypted OR
          NEW.error_encrypted IS DISTINCT FROM OLD.error_encrypted OR
          NEW.input_state_encrypted IS DISTINCT FROM OLD.input_state_encrypted THEN
      PERFORM pg_notify('gregale_mcp_task_' || md5(NEW.namespace), NEW.task_id::text);
    END IF;
    RETURN NEW;
  END;
  $$`;

const CREATE_TASK_NOTIFY_TRIGGER = `
  DO $body$
  BEGIN
    IF NOT EXISTS (
      SELECT 1 FROM pg_trigger
       WHERE tgrelid = '${TABLE}'::regclass
         AND tgname = 'gregale_mcp_tasks_notify'
         AND NOT tgisinternal
    ) THEN
      CREATE TRIGGER gregale_mcp_tasks_notify
        AFTER INSERT OR UPDATE ON ${TABLE}
        FOR EACH ROW EXECUTE FUNCTION gregale_mcp_task_notify_change();
    END IF;
  END;
  $body$`;

function encodeJSON(value, limit, label) {
  let encoded;
  try {
    encoded = Buffer.from(JSON.stringify(value));
  } catch {
    throw new Error(`${label} must be JSON serializable`);
  }
  if (encoded.length > limit) throw new Error(`${label} exceeds the ${limit}-byte MCP task limit`);
  return encoded;
}

function encryptJSON(value, key, aad, limit, label) {
  const plaintext = encodeJSON(value, limit, label);
  const nonce = randomBytes(12);
  const cipher = createCipheriv('aes-256-gcm', key, nonce);
  cipher.setAAD(Buffer.from(aad));
  const ciphertext = Buffer.concat([cipher.update(plaintext), cipher.final()]);
  return Buffer.concat([Buffer.from([1]), nonce, cipher.getAuthTag(), ciphertext]);
}

function decryptJSON(value, key, aad) {
  if (!Buffer.isBuffer(value) || value.length < 30 || value[0] !== 1) throw new Error('Invalid encrypted MCP task payload');
  const nonce = value.subarray(1, 13);
  const tag = value.subarray(13, 29);
  const ciphertext = value.subarray(29);
  const decipher = createDecipheriv('aes-256-gcm', key, nonce);
  decipher.setAAD(Buffer.from(aad));
  decipher.setAuthTag(tag);
  return JSON.parse(Buffer.concat([decipher.update(ciphertext), decipher.final()]).toString('utf8'));
}

function decryptInputState(value, key, namespace, taskID) {
  if (value == null) return { requests: Object.create(null), responses: Object.create(null) };
  const state = decryptJSON(value, key, taskAAD(namespace, taskID, 'input-state'));
  if (!state || typeof state !== 'object' || !state.requests || typeof state.requests !== 'object' || !state.responses || typeof state.responses !== 'object') {
    throw new Error('Invalid encrypted MCP task input state');
  }
  return {
    requests: Object.assign(Object.create(null), state.requests),
    responses: Object.assign(Object.create(null), state.responses),
  };
}

function outstandingRequests(state) {
  return Object.fromEntries(Object.entries(state.requests).filter(([key]) => !Object.hasOwn(state.responses, key)));
}

async function withTransaction(pool, callback) {
  const client = typeof pool.connect === 'function' ? await pool.connect() : pool;
  let inTransaction = false;
  try {
    await client.query('BEGIN');
    inTransaction = true;
    const result = await callback(client, client !== pool);
    await client.query('COMMIT');
    inTransaction = false;
    return result;
  } catch (error) {
    if (inTransaction) await client.query('ROLLBACK').catch(() => {});
    throw error;
  } finally {
    if (client !== pool) client.release();
  }
}

function taskAAD(namespace, taskID, field) {
  return `gregale-mcp-task:v1:${namespace}:${taskID}:${field}`;
}

function rowTask(row, payloadKey, namespace, includeArguments = false) {
  const taskID = String(row.task_id);
  const task = {
    task_id: taskID,
    tool_name: row.tool_name,
    handler_version: row.handler_version,
    status: row.status,
    created_at: row.created_at,
    updated_at: row.updated_at,
    expires_at: row.expires_at,
    attempt_count: row.attempt_count,
    lease_token: row.lease_token == null ? null : String(row.lease_token),
    cancel_requested_at: row.cancel_requested_at,
    input_methods: Array.isArray(row.input_methods) ? row.input_methods : [],
  };
  if (includeArguments) task.arguments = decryptJSON(row.arguments_encrypted, payloadKey, taskAAD(namespace, taskID, 'arguments'));
  if (row.result_encrypted != null) task.result = decryptJSON(row.result_encrypted, payloadKey, taskAAD(namespace, taskID, 'result'));
  if (row.error_encrypted != null) task.error = decryptJSON(row.error_encrypted, payloadKey, taskAAD(namespace, taskID, 'error'));
  if (row.input_state_encrypted != null) {
    task.input_requests = outstandingRequests(decryptInputState(row.input_state_encrypted, payloadKey, namespace, taskID));
  }
  return task;
}

function principalFor(authInfo, authMode) {
  if (authMode === 'open') return 'open';
  const subject = authInfo?.extra?.subject;
  if (typeof subject !== 'string' || !subject || subject.length > 4096) throw new Error('Authenticated MCP task owner is unavailable');
  const resource = authInfo.resource instanceof URL ? authInfo.resource.href : String(authInfo.resource || '');
  const clientId = typeof authInfo.clientId === 'string' ? authInfo.clientId : '';
  return JSON.stringify([resource, subject, clientId]);
}

export function createPostgresMcpTaskStore({ pool, namespace, ownerKey, ttlMs, maxOutstanding = defaults.maxOutstanding, maxOutstandingPerOwner = defaults.maxOutstandingPerOwner, maxRunning = defaults.maxRunning, maxRunningPerOwner = defaults.maxRunningPerOwner }) {
  if (!pool || typeof pool.query !== 'function') throw new Error('MCP Tasks require a PostgreSQL connection pool');
  if (typeof namespace !== 'string' || !namespace || namespace.length > 255) throw new Error('MCP Tasks require a stable app namespace');
  if (typeof ownerKey !== 'string' || Buffer.byteLength(ownerKey) < 32) throw new Error('MCP task owner key must contain at least 32 bytes');
  if (!Number.isSafeInteger(ttlMs) || ttlMs < 60_000 || ttlMs > 30 * 24 * 60 * 60 * 1000) throw new Error('MCP task TTL must be between one minute and 30 days');
  for (const limit of [maxOutstanding, maxOutstandingPerOwner]) {
    if (!Number.isSafeInteger(limit) || limit < 1) throw new Error('MCP task admission limits must be positive integers');
  }
  if (maxOutstandingPerOwner > maxOutstanding) throw new Error('MCP task owner limit must not exceed the namespace limit');

  validateRunningLimits(maxRunning, maxRunningPerOwner);

  const masterKey = Buffer.from(ownerKey);
  const ownerKeyBytes = createHmac('sha256', masterKey).update('gregale-mcp-task-owner:v1').digest();
  const payloadKey = createHmac('sha256', masterKey).update('gregale-mcp-task-payload:v1').digest();
  const ownerHash = (authInfo, authMode) => createHmac('sha256', ownerKeyBytes).update(principalFor(authInfo, authMode)).digest();
  // PostgreSQL channels are shared across every connection to a database. Keep
  // notifications scoped to this app namespace and put only the task UUID in
  // the payload so other app subscriptions cannot observe namespace metadata.
  const notificationChannel = `gregale_mcp_task_${createHash('md5').update(namespace).digest('hex')}`;
  const listeners = new Set();
  let listenClient;
  let listenNotificationHandler;
  let listenErrorHandler;
  let listenConnecting;
  let listenRetryTimer;
  let listenRetryDelayMs = 1000;

  function scheduleListenReconnect() {
    if (listeners.size === 0 || listenRetryTimer) return;
    listenRetryTimer = setTimeout(() => {
      listenRetryTimer = undefined;
      void startListening();
    }, listenRetryDelayMs);
    listenRetryDelayMs = Math.min(listenRetryDelayMs * 2, 30_000);
    listenRetryTimer.unref?.();
  }

  async function startListening() {
    if (listeners.size === 0 || listenClient) return;
    if (listenConnecting) return listenConnecting;
    listenConnecting = (async () => {
      let client;
      let notificationHandler;
      let errorHandler;
      try {
        client = await pool.connect();
        notificationHandler = message => {
          if (message.channel !== notificationChannel || typeof message.payload !== 'string' || !UUID_PATTERN.test(message.payload)) return;
          for (const listener of listeners) {
            try { listener({ taskID: message.payload }); } catch { /* A watcher must not break other subscribers. */ }
          }
        };
        errorHandler = error => {
          if (listenClient !== client) return;
          listenClient = undefined;
          listenNotificationHandler = undefined;
          listenErrorHandler = undefined;
          client.removeListener('notification', notificationHandler);
          client.removeListener('error', errorHandler);
          client.release(error);
          scheduleListenReconnect();
        };
        client.on('notification', notificationHandler);
        client.on('error', errorHandler);
        // The channel has a fixed prefix and lowercase hex digest, so it is a
        // safe SQL identifier even though PostgreSQL cannot bind LISTEN names.
        await client.query(`LISTEN ${notificationChannel}`);
        if (listeners.size === 0) {
          client.removeListener('notification', notificationHandler);
          client.removeListener('error', errorHandler);
          await client.query(`UNLISTEN ${notificationChannel}`).catch(() => {});
          client.release();
          return;
        }
        listenClient = client;
        listenNotificationHandler = notificationHandler;
        listenErrorHandler = errorHandler;
        clearTimeout(listenRetryTimer);
        listenRetryTimer = undefined;
        listenRetryDelayMs = 1000;
      } catch {
        if (client) {
          if (notificationHandler) client.removeListener('notification', notificationHandler);
          if (errorHandler) client.removeListener('error', errorHandler);
          client.release(true);
        }
        scheduleListenReconnect();
      } finally {
        listenConnecting = undefined;
      }
    })();
    return listenConnecting;
  }

  async function stopListeningIfIdle() {
    if (listeners.size > 0) return;
    clearTimeout(listenRetryTimer);
    listenRetryTimer = undefined;
    listenRetryDelayMs = 1000;
    const client = listenClient;
    listenClient = undefined;
    if (!client) return;
    client.removeListener('notification', listenNotificationHandler);
    client.removeListener('error', listenErrorHandler);
    listenNotificationHandler = undefined;
    listenErrorHandler = undefined;
    try {
      await client.query(`UNLISTEN ${notificationChannel}`);
    } catch {
      client.release(true);
      return;
    }
    client.release();
  }

  return {
    async initialize() {
      await withTransaction(pool, async (client, dedicated) => {
        if (dedicated) await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1, 0))', [TABLE]);
        await client.query(CREATE_SCHEMA);
        await client.query(MIGRATE_SCHEMA);
        await client.query(CREATE_QUEUE_INDEX);
        await client.query(CREATE_ADMISSION_INDEX);
        await client.query(`CREATE INDEX IF NOT EXISTS gregale_mcp_tasks_live_leases ON ${TABLE} (namespace, owner_hash, lease_expires_at) WHERE status = 'running'`);
        await client.query(CREATE_FAIRNESS_SCHEMA);
        await client.query(CREATE_FAIRNESS_SEQUENCE);
        await client.query(CREATE_FAIRNESS_INDEX);
        await client.query(CREATE_TASK_FAIRNESS_FUNCTION);
        // Install the seed trigger before backfilling so an older app process
        // cannot insert an owner between the backfill snapshot and trigger setup.
        await client.query(CREATE_TASK_FAIRNESS_TRIGGER);
        await client.query(`
          INSERT INTO ${FAIRNESS_TABLE} (namespace, owner_hash)
          SELECT namespace, owner_hash
            FROM ${TABLE}
           WHERE namespace = $1 AND expires_at > clock_timestamp()
             AND status IN ('queued', 'running', 'input_required')
           GROUP BY namespace, owner_hash
          ON CONFLICT (namespace, owner_hash) DO NOTHING
        `, [namespace]);
        await client.query(CREATE_TASK_NOTIFY_FUNCTION);
        await client.query(CREATE_TASK_NOTIFY_TRIGGER);
      });
    },
    queueMetrics: createMcpTaskQueueObserver({ pool, namespace, maxRunning, maxRunningPerOwner }).queueMetrics,
    async create({ toolName, handlerVersion, args, authInfo, authMode, inputMethods = [] }) {
      if (typeof toolName !== 'string' || !/^[a-z][a-z0-9_.-]{0,127}$/.test(toolName)) throw new Error('Invalid MCP task tool name');
      if (typeof handlerVersion !== 'string' || !/^[A-Za-z0-9._-]{1,64}$/.test(handlerVersion)) throw new Error('Invalid MCP task handler version');
      if (!Array.isArray(inputMethods) || inputMethods.some(method => !SUPPORTED_INPUT_METHODS.has(method))) throw new Error('Invalid MCP task input capabilities');
      const taskID = randomUUID();
      const encryptedArgs = encryptJSON(args, payloadKey, taskAAD(namespace, taskID, 'arguments'), MAX_ARGUMENT_BYTES, 'MCP task arguments');
      const owner = ownerHash(authInfo, authMode);
      const result = await withTransaction(pool, async client => {
        // Serialize admission across web replicas, using the same database clock
        // and transaction as the insert. Waiting for input still consumes capacity.
        await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1, 0))', [`${TABLE}:admission:${namespace}`]);
        const counts = await client.query(`
          SELECT COUNT(*)::int AS total, COUNT(*) FILTER (WHERE owner_hash = $2)::int AS owned
            FROM ${TABLE} WHERE namespace = $1 AND expires_at > clock_timestamp()
             AND status IN ('queued', 'running', 'input_required')
        `, [namespace, owner]);
        if (counts.rows[0].total >= maxOutstanding || counts.rows[0].owned >= maxOutstandingPerOwner) {
          const error = new Error('MCP task queue capacity reached; retry after outstanding tasks finish');
          error.code = 'MCP_TASK_CAPACITY';
          throw error;
        }
        return client.query(`
        INSERT INTO ${TABLE} (
          namespace, task_id, owner_hash, tool_name, handler_version,
          arguments_encrypted, input_methods, status, expires_at
        ) VALUES ($1, $2::uuid, $3, $4, $5, $6, $7::text[], 'queued', clock_timestamp() + ($8::double precision * interval '1 millisecond'))
        RETURNING task_id::text AS task_id, tool_name, handler_version, status,
                  created_at, updated_at, expires_at, attempt_count,
                  lease_token, cancel_requested_at, input_methods
        `, [namespace, taskID, owner, toolName, handlerVersion, encryptedArgs, inputMethods, ttlMs]);
      });
      if (!result.rows?.[0]) throw new Error('Could not persist MCP task');
      return rowTask(result.rows[0], payloadKey, namespace);
    },
    async get({ taskID, authInfo, authMode }) {
      if (typeof taskID !== 'string' || !UUID_PATTERN.test(taskID)) return null;
      const result = await pool.query(`
        SELECT task_id::text AS task_id, tool_name, handler_version, status,
               created_at, updated_at, expires_at, attempt_count,
               lease_token, cancel_requested_at, result_encrypted, error_encrypted,
               input_state_encrypted, input_methods
          FROM ${TABLE}
         WHERE namespace = $1 AND task_id = $2::uuid AND owner_hash = $3
           AND expires_at > clock_timestamp()
      `, [namespace, taskID, ownerHash(authInfo, authMode)]);
      return result.rows?.[0] ? rowTask(result.rows[0], payloadKey, namespace) : null;
    },
    async getMany({ taskIDs, authInfo, authMode }) {
      const ids = [...new Set((Array.isArray(taskIDs) ? taskIDs : []).filter(taskID => typeof taskID === 'string' && UUID_PATTERN.test(taskID)))];
      if (ids.length === 0) return [];
      const result = await pool.query(`
        SELECT task_id::text AS task_id, tool_name, handler_version, status,
               created_at, updated_at, expires_at, attempt_count,
               lease_token, cancel_requested_at, result_encrypted, error_encrypted,
               input_state_encrypted, input_methods
          FROM ${TABLE}
         WHERE namespace = $1 AND task_id = ANY($2::uuid[]) AND owner_hash = $3
           AND expires_at > clock_timestamp()
      `, [namespace, ids, ownerHash(authInfo, authMode)]);
      return (result.rows || []).map(row => rowTask(row, payloadKey, namespace));
    },
    async subscribe(listener) {
      if (typeof listener !== 'function') throw new Error('MCP task store subscriptions require a listener');
      listeners.add(listener);
      await startListening();
      let active = true;
      return async () => {
        if (!active) return;
        active = false;
        listeners.delete(listener);
        await stopListeningIfIdle();
      };
    },
    async requestInputs({ taskID, leaseToken, requests }) {
      const entries = Object.entries(requests || {});
      return withTransaction(pool, async client => {
        const selected = await client.query(`
          SELECT input_state_encrypted, cancel_requested_at
            FROM ${TABLE}
           WHERE namespace = $1 AND task_id = $2::uuid AND lease_token = $3::uuid
             AND status = 'running'
           FOR UPDATE
        `, [namespace, taskID, leaseToken]);
        if (!selected.rows?.[0]) return { ready: false, responses: {} };
        if (selected.rows[0].cancel_requested_at != null) return { ready: false, cancelled: true, responses: {} };

        const state = decryptInputState(selected.rows[0].input_state_encrypted, payloadKey, namespace, taskID);
        for (const [key, request] of entries) {
          if (Object.hasOwn(state.requests, key)) {
            if (!isDeepStrictEqual(state.requests[key], request)) throw new Error('MCP task input request keys cannot be reused for different requests');
          } else {
            state.requests[key] = request;
          }
        }
        if (Object.keys(state.requests).length > 64) throw new Error('MCP task exceeds the 64 unique input request limit');

        const responses = Object.fromEntries(entries.filter(([key]) => Object.hasOwn(state.responses, key)).map(([key]) => [key, state.responses[key]]));
        const pending = entries.some(([key]) => !Object.hasOwn(state.responses, key));
        if (pending) {
          const encrypted = encryptJSON(state, payloadKey, taskAAD(namespace, taskID, 'input-state'), MAX_INPUT_STATE_BYTES, 'MCP task input state');
          await client.query(`
            UPDATE ${TABLE}
               SET status = 'input_required', input_state_encrypted = $4,
                   lease_token = NULL, lease_expires_at = NULL, updated_at = clock_timestamp()
             WHERE namespace = $1 AND task_id = $2::uuid AND lease_token = $3::uuid AND status = 'running'
          `, [namespace, taskID, leaseToken, encrypted]);
          return { ready: false, responses: {} };
        }
        return { ready: true, responses };
      });
    },
    async updateInputs({ taskID, authInfo, authMode, inputResponses }) {
      if (typeof taskID !== 'string' || !UUID_PATTERN.test(taskID)) return false;
      if (!inputResponses || typeof inputResponses !== 'object' || Array.isArray(inputResponses)) throw new Error('MCP task input responses must be an object');
      const responseEntries = Object.entries(inputResponses);
      if (responseEntries.length > 64 || responseEntries.some(([key]) => !key || key.length > 128)) throw new Error('Invalid MCP task input response keys');
      encodeJSON(inputResponses, MAX_INPUT_STATE_BYTES, 'MCP task input responses');

      return withTransaction(pool, async client => {
        const selected = await client.query(`
          SELECT status, input_state_encrypted
            FROM ${TABLE}
           WHERE namespace = $1 AND task_id = $2::uuid AND owner_hash = $3
             AND expires_at > clock_timestamp()
           FOR UPDATE
        `, [namespace, taskID, ownerHash(authInfo, authMode)]);
        const row = selected.rows?.[0];
        if (!row) return false;
        if (row.status !== 'input_required' || row.input_state_encrypted == null) return true;

        const state = decryptInputState(row.input_state_encrypted, payloadKey, namespace, taskID);
        let changed = false;
        for (const [key, response] of responseEntries) {
          if (!Object.hasOwn(state.requests, key) || Object.hasOwn(state.responses, key)) continue;
          state.responses[key] = response;
          changed = true;
        }
        if (!changed) return true;

        const pending = Object.keys(state.requests).some(key => !Object.hasOwn(state.responses, key));
        const encrypted = encryptJSON(state, payloadKey, taskAAD(namespace, taskID, 'input-state'), MAX_INPUT_STATE_BYTES, 'MCP task input state');
        await client.query(`
          UPDATE ${TABLE}
             SET input_state_encrypted = $3,
                 status = CASE WHEN $4::boolean THEN 'input_required' ELSE 'queued' END,
                 resume_pending = NOT $4::boolean,
                 updated_at = clock_timestamp()
           WHERE namespace = $1 AND task_id = $2::uuid AND status = 'input_required'
        `, [namespace, taskID, encrypted, pending]);
        return true;
      });
    },
    async requestCancel({ taskID, authInfo, authMode }) {
      if (typeof taskID !== 'string' || !UUID_PATTERN.test(taskID)) return false;
      const result = await pool.query(`
        UPDATE ${TABLE}
           SET status = CASE WHEN status IN ('queued', 'input_required') THEN 'cancelled' ELSE status END,
               cancel_requested_at = CASE WHEN status IN ('queued', 'running', 'input_required') THEN COALESCE(cancel_requested_at, clock_timestamp()) ELSE cancel_requested_at END,
               updated_at = CASE WHEN status IN ('queued', 'running', 'input_required') THEN clock_timestamp() ELSE updated_at END,
               lease_token = CASE WHEN status IN ('queued', 'input_required') THEN NULL ELSE lease_token END,
               lease_expires_at = CASE WHEN status IN ('queued', 'input_required') THEN NULL ELSE lease_expires_at END
         WHERE namespace = $1 AND task_id = $2::uuid AND owner_hash = $3
           AND expires_at > clock_timestamp()
         RETURNING task_id
      `, [namespace, taskID, ownerHash(authInfo, authMode)]);
      return (result.rowCount ?? result.rows?.length ?? 0) > 0;
    },
    async claim(maxAttempts, leaseMs, supportedHandlers) {
      await pool.query(`
        UPDATE ${TABLE}
           SET status = CASE WHEN cancel_requested_at IS NOT NULL THEN 'cancelled' ELSE 'failed' END,
               error_encrypted = NULL,
               updated_at = clock_timestamp(), lease_token = NULL, lease_expires_at = NULL
         WHERE namespace = $1 AND status = 'running'
           AND lease_expires_at <= clock_timestamp() AND attempt_count >= $2
      `, [namespace, maxAttempts]);
      const leaseToken = randomUUID();
      // Capacity checks and lease creation share a namespace transaction lock.
      let result;
      for (let attempt = 0; attempt < FAIRNESS_CLAIM_RETRIES; attempt++) {
        result = await withTransaction(pool, async client => {
          await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1, 0))', [`${TABLE}:execution:${namespace}`]);
          return client.query(`
        WITH chosen_owner AS MATERIALIZED (
          SELECT fairness.namespace, fairness.owner_hash
            FROM ${FAIRNESS_TABLE} AS fairness
           WHERE fairness.namespace = $1
             AND ${RUNNING_CAPACITY_FILTER}
             AND EXISTS (
               SELECT 1 FROM ${TABLE} AS task
                WHERE task.owner_hash = fairness.owner_hash
                  AND ${CLAIMABLE_TASK_FILTER}
             )
           ORDER BY fairness.last_claimed_order, fairness.owner_hash
           FOR UPDATE OF fairness SKIP LOCKED
           LIMIT 1
        ),
        candidate AS MATERIALIZED (
          SELECT task.namespace, task.task_id
            FROM ${TABLE} AS task
            JOIN chosen_owner AS chosen USING (namespace, owner_hash)
           WHERE ${CLAIMABLE_TASK_FILTER}
           ORDER BY task.created_at, task.task_id
           FOR UPDATE OF task SKIP LOCKED
           LIMIT 1
        ),
        claimed AS (
          UPDATE ${TABLE} AS task
             SET status = 'running',
                 attempt_count = task.attempt_count + CASE WHEN task.resume_pending THEN 0 ELSE 1 END,
                 resume_pending = false,
                 next_attempt_at = NULL,
                 lease_token = $3::uuid,
                 lease_expires_at = clock_timestamp() + ($4::bigint * interval '1 millisecond'),
                 updated_at = clock_timestamp()
            FROM candidate
           WHERE task.namespace = candidate.namespace AND task.task_id = candidate.task_id
          RETURNING task.namespace, task.task_id, task.owner_hash, task.tool_name, task.handler_version,
                    task.status, task.created_at, task.updated_at, task.expires_at,
                    task.attempt_count, task.lease_token::text AS lease_token,
                    task.cancel_requested_at, task.arguments_encrypted,
                    task.result_encrypted, task.error_encrypted,
                    task.input_state_encrypted, task.input_methods
        ),
        advanced AS (
          UPDATE ${FAIRNESS_TABLE} AS fairness
             SET last_claimed_order = nextval('${FAIRNESS_SEQUENCE}')
            FROM claimed
           WHERE fairness.namespace = claimed.namespace
             AND fairness.owner_hash = claimed.owner_hash
          RETURNING fairness.namespace, fairness.owner_hash
        )
        SELECT claimed.task_id::text AS task_id, claimed.tool_name, claimed.handler_version,
               claimed.status, claimed.created_at, claimed.updated_at, claimed.expires_at,
               claimed.attempt_count, claimed.lease_token,
               claimed.cancel_requested_at, claimed.arguments_encrypted,
               claimed.result_encrypted, claimed.error_encrypted,
               claimed.input_state_encrypted, claimed.input_methods, false AS busy
          FROM claimed
          JOIN advanced USING (namespace, owner_hash)
        UNION ALL
        SELECT NULL::text, NULL::text, NULL::text, NULL::text,
               NULL::timestamptz, NULL::timestamptz, NULL::timestamptz,
               NULL::integer, NULL::text, NULL::timestamptz,
               NULL::bytea, NULL::bytea, NULL::bytea, NULL::bytea,
               NULL::text[], true AS busy
         WHERE NOT EXISTS (SELECT 1 FROM claimed)
           AND EXISTS (
             SELECT 1 FROM ${FAIRNESS_TABLE} AS fairness
              WHERE fairness.namespace = $1
                AND ${RUNNING_CAPACITY_FILTER}
                AND EXISTS (
                  SELECT 1 FROM ${TABLE} AS task
                   WHERE task.owner_hash = fairness.owner_hash
                     AND ${CLAIMABLE_TASK_FILTER}
                )
           )
        `, [namespace, maxAttempts, leaseToken, leaseMs, supportedHandlers == null ? null : JSON.stringify(supportedHandlers), maxRunning, maxRunningPerOwner]);
        });
        const row = result.rows?.[0];
        if (!row?.busy || attempt === FAIRNESS_CLAIM_RETRIES - 1) break;
        // An empty result with eligible work means every matching owner cursor
        // is briefly locked by another claim. Retry promptly instead of making
        // this worker wait for its normal queue-poll interval.
        await delay(Math.min(2 ** attempt, 10));
      }
      const task = result.rows?.[0];
      return task && !task.busy ? rowTask(task, payloadKey, namespace, true) : null;
    },
    async heartbeat(taskID, leaseToken, leaseMs) {
      const result = await withTransaction(pool, async client => {
        await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1, 0))', [`${TABLE}:execution:${namespace}`]);
        return client.query(`
        UPDATE ${TABLE}
           SET lease_expires_at = clock_timestamp() + ($4::bigint * interval '1 millisecond')
         WHERE namespace = $1 AND task_id = $2::uuid AND lease_token = $3::uuid AND status = 'running'
           AND lease_expires_at > clock_timestamp() AND expires_at > clock_timestamp()
         RETURNING cancel_requested_at
      `, [namespace, taskID, leaseToken, leaseMs]);
      });
      if (!result.rows?.[0]) return { owned: false, cancelRequested: false };
      return { owned: true, cancelRequested: result.rows[0].cancel_requested_at != null };
    },
    async complete(taskID, leaseToken, value) {
      const encrypted = encryptJSON(value, payloadKey, taskAAD(namespace, taskID, 'result'), MAX_RESULT_BYTES, 'MCP task result');
      const result = await pool.query(`
        UPDATE ${TABLE}
           SET status = CASE WHEN cancel_requested_at IS NULL THEN 'completed' ELSE 'cancelled' END,
               result_encrypted = CASE WHEN cancel_requested_at IS NULL THEN $4::bytea ELSE NULL END,
               error_encrypted = NULL, updated_at = clock_timestamp(),
               lease_token = NULL, lease_expires_at = NULL
         WHERE namespace = $1 AND task_id = $2::uuid AND lease_token = $3::uuid AND status = 'running'
         RETURNING status
      `, [namespace, taskID, leaseToken, encrypted]);
      return result.rows?.[0]?.status ?? null;
    },
    async fail(taskID, leaseToken, error, { retryable = false, maxAttempts = 3, retryDelayMs = 1000 } = {}) {
      if (typeof retryable !== 'boolean' || !Number.isSafeInteger(maxAttempts) || maxAttempts < 1 || maxAttempts > 10 || !Number.isSafeInteger(retryDelayMs) || retryDelayMs < 1 || retryDelayMs > 86_400_000) throw new Error('Invalid MCP task retry policy');
      const encrypted = encryptJSON(error, payloadKey, taskAAD(namespace, taskID, 'error'), MAX_RESULT_BYTES, 'MCP task error');
      const result = await pool.query(`
        WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS now)
        UPDATE ${TABLE}
           SET status = CASE WHEN cancel_requested_at IS NOT NULL THEN 'cancelled'
                             WHEN $5 AND attempt_count < $6 AND instant.now + ($7::bigint * interval '1 millisecond') < expires_at THEN 'queued'
                             ELSE 'failed' END,
               next_attempt_at = CASE WHEN cancel_requested_at IS NULL AND $5 AND attempt_count < $6 AND instant.now + ($7::bigint * interval '1 millisecond') < expires_at
                                      THEN instant.now + ($7::bigint * interval '1 millisecond') ELSE NULL END,
               error_encrypted = CASE WHEN cancel_requested_at IS NULL THEN $4::bytea ELSE NULL END,
               result_encrypted = NULL, updated_at = instant.now,
               resume_pending = false, lease_token = NULL, lease_expires_at = NULL
          FROM instant
         WHERE namespace = $1 AND task_id = $2::uuid AND lease_token = $3::uuid AND status = 'running'
           AND lease_expires_at > clock_timestamp() AND expires_at > clock_timestamp()
         RETURNING status
      `, [namespace, taskID, leaseToken, encrypted, retryable, maxAttempts, retryDelayMs]);
      return result.rows?.[0]?.status ?? null;
    },
    async finishCancelled(taskID, leaseToken) {
      const result = await pool.query(`
        UPDATE ${TABLE}
           SET status = 'cancelled', result_encrypted = NULL, error_encrypted = NULL,
               updated_at = clock_timestamp(), lease_token = NULL, lease_expires_at = NULL
         WHERE namespace = $1 AND task_id = $2::uuid AND lease_token = $3::uuid AND status = 'running'
         RETURNING status
      `, [namespace, taskID, leaseToken]);
      return result.rows?.[0]?.status ?? null;
    },
    async cleanupExpired(limit = 250) {
      await withTransaction(pool, async client => {
        // Serialize pruning with admission so a just-created owner's cursor
        // cannot be removed before its task row becomes visible.
        await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1, 0))', [`${TABLE}:admission:${namespace}`]);
        await client.query(`
          WITH expired AS (
            SELECT namespace, task_id FROM ${TABLE}
             WHERE namespace = $1 AND expires_at <= clock_timestamp()
             ORDER BY expires_at LIMIT $2
          )
          DELETE FROM ${TABLE} AS task
           USING expired
           WHERE task.namespace = expired.namespace AND task.task_id = expired.task_id
        `, [namespace, limit]);
        await client.query(`
          DELETE FROM ${FAIRNESS_TABLE} AS fairness
           WHERE fairness.namespace = $1
             AND NOT EXISTS (
               SELECT 1 FROM ${TABLE} AS task
                WHERE task.namespace = fairness.namespace
                  AND task.owner_hash = fairness.owner_hash
                  AND task.expires_at > clock_timestamp()
                  AND task.status IN ('queued', 'running', 'input_required')
             )
        `, [namespace]);
      });
    },
  };
}

function validateRunningLimits(total, owner) {
  if (![total, owner].every(value => Number.isSafeInteger(value) && value > 0)) throw new Error('MCP task running limits must be positive integers');
  if (owner > total) throw new Error('MCP task running owner limit must not exceed the namespace limit');
}

// Read-only observers never initialize schema, decrypt payloads, or claim work.
export function createMcpTaskQueueObserver({ pool, namespace, maxRunning = defaults.maxRunning, maxRunningPerOwner = defaults.maxRunningPerOwner }) {
  validateRunningLimits(maxRunning, maxRunningPerOwner);
  async function queueMetrics() {
    const result = await pool.query(`
      WITH instant AS MATERIALIZED (SELECT clock_timestamp() AS now),
      retained AS MATERIALIZED (
        SELECT task.owner_hash, task.status, task.created_at, task.next_attempt_at, instant.now,
               status = 'running' AND lease_expires_at > instant.now AS live
          FROM ${TABLE} AS task CROSS JOIN instant
         WHERE namespace = $1 AND expires_at > instant.now AND status IN ('queued', 'running', 'failed')
      ), pending AS MATERIALIZED (SELECT * FROM retained WHERE status <> 'failed'), owners AS (
        SELECT owner_hash, COUNT(*) FILTER (WHERE live) AS running FROM pending GROUP BY owner_hash
      ), totals AS (SELECT COUNT(*) FILTER (WHERE live) AS running FROM pending)
      SELECT COUNT(*)::text AS outstanding_tasks,
             COALESCE(GREATEST(EXTRACT(EPOCH FROM ((SELECT now FROM instant) - MIN(created_at))), 0), 0)::text AS oldest_age_seconds,
             COUNT(*) FILTER (WHERE live)::text AS running_tasks,
             COUNT(*) FILTER (WHERE NOT live AND (next_attempt_at IS NULL OR next_attempt_at <= now) AND (totals.running >= $2 OR owners.running >= $3))::text AS capacity_waiting_tasks,
             (SELECT COUNT(*) FROM retained WHERE status = 'failed')::text AS failed_tasks,
             COUNT(*) FILTER (WHERE status = 'queued' AND next_attempt_at > now)::text AS retry_waiting_tasks
        FROM pending JOIN owners USING (owner_hash) CROSS JOIN totals
    `, [namespace, maxRunning, maxRunningPerOwner]);
    const row = result.rows?.[0];
    if (!row) throw new Error('Could not read MCP task queue metrics');
    const metrics = { outstandingTasks: Number(row.outstanding_tasks), oldestAgeSeconds: Number(row.oldest_age_seconds), runningTasks: Number(row.running_tasks), capacityWaitingTasks: Number(row.capacity_waiting_tasks), failedTasks: Number(row.failed_tasks), retryWaitingTasks: Number(row.retry_waiting_tasks) };
    if (!['outstandingTasks', 'runningTasks', 'capacityWaitingTasks', 'failedTasks', 'retryWaitingTasks'].every(key => Number.isSafeInteger(metrics[key]) && metrics[key] >= 0) || !Number.isFinite(metrics.oldestAgeSeconds) || metrics.oldestAgeSeconds < 0) throw new Error('MCP task queue metrics returned invalid values');
    return metrics;
  }
  return { queueMetrics };
}
