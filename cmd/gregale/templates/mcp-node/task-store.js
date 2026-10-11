import { createCipheriv, createDecipheriv, createHash, createHmac, randomBytes, randomUUID } from 'node:crypto';
import { isDeepStrictEqual } from 'node:util';
import defaults from './task-limits.json' with { type: 'json' };

const TABLE = 'gregale_mcp_tasks';
const WORKER_HANDLERS_TABLE = 'gregale_mcp_task_worker_handlers';
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
    lease_token uuid,
    lease_expires_at timestamptz,
    cancel_requested_at timestamptz,
    PRIMARY KEY (namespace, task_id),
    CHECK ((status = 'running') = (lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)),
    CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL))
  )`;

const CREATE_WORKER_HANDLERS_SCHEMA = `
  CREATE TABLE IF NOT EXISTS ${WORKER_HANDLERS_TABLE} (
    namespace text NOT NULL,
    worker_id uuid NOT NULL,
    tool_name text NOT NULL,
    handler_version text NOT NULL,
    deployment_id text NOT NULL DEFAULT '',
    heartbeat_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (namespace, worker_id, tool_name, handler_version)
  );
  CREATE INDEX IF NOT EXISTS gregale_mcp_task_worker_handlers_live_idx
    ON ${WORKER_HANDLERS_TABLE} (namespace, tool_name, handler_version, heartbeat_at DESC);
  CREATE INDEX IF NOT EXISTS gregale_mcp_task_worker_handlers_stale_idx
    ON ${WORKER_HANDLERS_TABLE} (namespace, heartbeat_at)`;

const MIGRATE_SCHEMA = `
  ALTER TABLE ${WORKER_HANDLERS_TABLE}
    ADD COLUMN IF NOT EXISTS deployment_id text NOT NULL DEFAULT '';
  ALTER TABLE ${TABLE}
    ADD COLUMN IF NOT EXISTS input_state_encrypted bytea,
    ADD COLUMN IF NOT EXISTS input_methods text[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS resume_pending boolean NOT NULL DEFAULT false;
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

const CREATE_TASK_NOTIFY_FUNCTION = `
  CREATE OR REPLACE FUNCTION gregale_mcp_task_notify_change() RETURNS trigger
  LANGUAGE plpgsql AS $$
  BEGIN
    IF TG_OP = 'INSERT' THEN
      PERFORM pg_notify('gregale_mcp_task_' || md5(NEW.namespace), NEW.task_id::text);
    ELSIF current_setting('gregale.mcp_task_rewrap', true) IS DISTINCT FROM 'on' AND (
          NEW.status IS DISTINCT FROM OLD.status OR
          NEW.updated_at IS DISTINCT FROM OLD.updated_at OR
          NEW.result_encrypted IS DISTINCT FROM OLD.result_encrypted OR
          NEW.error_encrypted IS DISTINCT FROM OLD.error_encrypted OR
          NEW.input_state_encrypted IS DISTINCT FROM OLD.input_state_encrypted) THEN
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

function encryptPayload(plaintext, key, keyID, aad, formatVersion = 2) {
  const keyIDBytes = Buffer.from(keyID, 'ascii');
  const nonce = randomBytes(12);
  const cipher = createCipheriv('aes-256-gcm', key, nonce);
  cipher.setAAD(Buffer.from(aad));
  const ciphertext = Buffer.concat([cipher.update(plaintext), cipher.final()]);
  if (formatVersion === 1) return Buffer.concat([Buffer.from([1]), nonce, cipher.getAuthTag(), ciphertext]);
  return Buffer.concat([Buffer.from([2, keyIDBytes.length]), keyIDBytes, nonce, cipher.getAuthTag(), ciphertext]);
}

function encryptJSON(value, key, keyID, aad, limit, label, formatVersion) {
  return encryptPayload(encodeJSON(value, limit, label), key, keyID, aad, formatVersion);
}

function decryptPayload(value, keyring, aad) {
  if (!Buffer.isBuffer(value) || value.length < 30) throw new Error('Invalid encrypted MCP task payload');
  let offset;
  let candidates;
  if (value[0] === 1) {
    offset = 1;
    candidates = keyring.keys;
  } else if (value[0] === 2) {
    const keyIDLength = value[1];
    if (!keyIDLength || keyIDLength > 64 || value.length < 2 + keyIDLength + 28) throw new Error('Invalid encrypted MCP task payload');
    const keyIDBytes = value.subarray(2, 2 + keyIDLength);
    if ([...keyIDBytes].some(byte => byte > 0x7f)) throw new Error('Invalid encrypted MCP task payload');
    const keyID = keyIDBytes.toString('ascii');
    if (!/^[A-Za-z0-9._-]{1,64}$/.test(keyID)) throw new Error('Invalid encrypted MCP task payload');
    const key = keyring.byID.get(keyID);
    if (!key) throw new Error('Unknown MCP task payload key ID');
    offset = 2 + keyIDLength;
    candidates = [key];
  } else {
    throw new Error('Invalid encrypted MCP task payload');
  }
  if (value.length < offset + 28) throw new Error('Invalid encrypted MCP task payload');
  const nonce = value.subarray(offset, offset + 12);
  const tag = value.subarray(offset + 12, offset + 28);
  const ciphertext = value.subarray(offset + 28);
  for (const key of candidates) {
    try {
      const decipher = createDecipheriv('aes-256-gcm', key, nonce);
      decipher.setAAD(Buffer.from(aad));
      decipher.setAuthTag(tag);
      return Buffer.concat([decipher.update(ciphertext), decipher.final()]);
    } catch { /* V1 payloads do not identify their key; try the retained keys. */ }
  }
  throw new Error('Could not decrypt MCP task payload with configured keys');
}

function decryptJSON(value, keyring, aad) {
  return JSON.parse(decryptPayload(value, keyring, aad).toString('utf8'));
}

function decryptInputState(value, keyring, namespace, taskID) {
  if (value == null) return { requests: Object.create(null), responses: Object.create(null) };
  const state = decryptJSON(value, keyring, taskAAD(namespace, taskID, 'input-state'));
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

function needsPayloadRewrapSQL(column) {
  return `(${column} IS NOT NULL AND CASE
    WHEN octet_length(${column}) < 2 THEN TRUE
    WHEN get_byte(${column}, 0) <> 2 THEN TRUE
    WHEN get_byte(${column}, 1) = 0 OR get_byte(${column}, 1) > 64 THEN TRUE
    WHEN octet_length(${column}) < 2 + get_byte(${column}, 1) + 28 THEN TRUE
    ELSE substring(${column} FROM 3 FOR get_byte(${column}, 1)) <> $2::bytea
  END)`;
}

function rowTask(row, payloadKeyring, namespace, includeArguments = false) {
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
  if (includeArguments) task.arguments = decryptJSON(row.arguments_encrypted, payloadKeyring, taskAAD(namespace, taskID, 'arguments'));
  if (row.result_encrypted != null) task.result = decryptJSON(row.result_encrypted, payloadKeyring, taskAAD(namespace, taskID, 'result'));
  if (row.error_encrypted != null) task.error = decryptJSON(row.error_encrypted, payloadKeyring, taskAAD(namespace, taskID, 'error'));
  if (row.input_state_encrypted != null) {
    task.input_requests = outstandingRequests(decryptInputState(row.input_state_encrypted, payloadKeyring, namespace, taskID));
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

function ownerHashPredicate(hashes, firstParameter) {
  return `owner_hash IN (${hashes.map((_, index) => `decode($${firstParameter + index}, 'hex')`).join(', ')})`;
}

export function createPostgresMcpTaskStore({ pool, namespace, ownerKey, ownerKeyID = 'default', previousOwnerKeys = [], payloadEncryptionVersion = 1, ttlMs, maxOutstanding = defaults.maxOutstanding, maxOutstandingPerOwner = defaults.maxOutstandingPerOwner }) {
  if (!pool || typeof pool.query !== 'function') throw new Error('MCP Tasks require a PostgreSQL connection pool');
  if (typeof namespace !== 'string' || !namespace || namespace.length > 255) throw new Error('MCP Tasks require a stable app namespace');
  const keyIDPattern = /^[A-Za-z0-9._-]{1,64}$/;
  if (typeof ownerKey !== 'string' || Buffer.byteLength(ownerKey) < 32) throw new Error('MCP task owner key must contain at least 32 bytes');
  if (typeof ownerKeyID !== 'string' || !keyIDPattern.test(ownerKeyID)) throw new Error('Invalid MCP task owner key ID');
  if (!Array.isArray(previousOwnerKeys) || previousOwnerKeys.length > 16) throw new Error('MCP task previous_owner_keys must contain at most 16 keys');
  if (payloadEncryptionVersion !== 1 && payloadEncryptionVersion !== 2) throw new Error('MCP task payload encryption version must be 1 or 2');
  if (!Number.isSafeInteger(ttlMs) || ttlMs < 60_000 || ttlMs > 30 * 24 * 60 * 60 * 1000) throw new Error('MCP task TTL must be between one minute and 30 days');
  for (const limit of [maxOutstanding, maxOutstandingPerOwner]) {
    if (!Number.isSafeInteger(limit) || limit < 1) throw new Error('MCP task admission limits must be positive integers');
  }
  if (maxOutstandingPerOwner > maxOutstanding) throw new Error('MCP task owner limit must not exceed the namespace limit');

  const keySpecs = [{ id: ownerKeyID, key: ownerKey }, ...previousOwnerKeys];
  const seenKeyIDs = new Set();
  const ownerKeys = keySpecs.map(spec => {
    if (!spec || typeof spec.id !== 'string' || !keyIDPattern.test(spec.id) || seenKeyIDs.has(spec.id)) throw new Error('MCP task owner key IDs must be valid and unique');
    if (typeof spec.key !== 'string' || Buffer.byteLength(spec.key) < 32) throw new Error(`MCP task owner key ${spec.id} must contain at least 32 bytes`);
    seenKeyIDs.add(spec.id);
    const masterKey = Buffer.from(spec.key);
    return {
      id: spec.id,
      ownerHashKey: createHmac('sha256', masterKey).update('gregale-mcp-task-owner:v1').digest(),
      payloadKey: createHmac('sha256', masterKey).update('gregale-mcp-task-payload:v1').digest(),
    };
  });
  const activeOwnerKey = ownerKeys[0];
  const payloadKeyring = { activeKeyID: ownerKeyID, active: activeOwnerKey.payloadKey, keys: ownerKeys.map(key => key.payloadKey), byID: new Map(ownerKeys.map(key => [key.id, key.payloadKey])) };
  const ownerHashes = (authInfo, authMode) => {
    const principal = principalFor(authInfo, authMode);
    return ownerKeys.map(key => createHmac('sha256', key.ownerHashKey).update(principal).digest());
  };
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
        await client.query(CREATE_WORKER_HANDLERS_SCHEMA);
        await client.query(MIGRATE_SCHEMA);
        await client.query(CREATE_QUEUE_INDEX);
        await client.query(CREATE_ADMISSION_INDEX);
        await client.query(CREATE_TASK_NOTIFY_FUNCTION);
        await client.query(CREATE_TASK_NOTIFY_TRIGGER);
      });
    },
    queueMetrics: createQueueMetricsObserver({ pool, namespace, verifyReadOnly: false }).queueMetrics,
    async heartbeatWorkerCapabilities(workerID, handlers, deploymentID = '') {
      if (!Array.isArray(handlers) || handlers.length === 0) return;
      const names = handlers.map(handler => handler.name);
      const versions = handlers.map(handler => handler.version);
      await pool.query(`
        INSERT INTO ${WORKER_HANDLERS_TABLE}(namespace,worker_id,tool_name,handler_version,deployment_id,heartbeat_at)
        SELECT $1,$2::uuid,handler.tool_name,handler.handler_version,$5,clock_timestamp()
          FROM unnest($3::text[],$4::text[]) AS handler(tool_name,handler_version)
        ON CONFLICT (namespace,worker_id,tool_name,handler_version)
        DO UPDATE SET deployment_id=EXCLUDED.deployment_id,heartbeat_at=EXCLUDED.heartbeat_at
      `, [namespace, workerID, names, versions, deploymentID]);
    },
    async removeWorkerCapabilities(workerID) {
      await pool.query(`DELETE FROM ${WORKER_HANDLERS_TABLE} WHERE namespace=$1 AND worker_id=$2::uuid`, [namespace, workerID]);
    },
    async cleanupStaleWorkerCapabilities() {
      await pool.query(`DELETE FROM ${WORKER_HANDLERS_TABLE} WHERE namespace=$1 AND heartbeat_at < clock_timestamp()-interval '1 day'`, [namespace]);
    },
    async create({ toolName, handlerVersion, args, authInfo, authMode, inputMethods = [] }) {
      if (typeof toolName !== 'string' || !/^[a-z][a-z0-9_.-]{0,127}$/.test(toolName)) throw new Error('Invalid MCP task tool name');
      if (typeof handlerVersion !== 'string' || !/^[A-Za-z0-9._-]{1,64}$/.test(handlerVersion)) throw new Error('Invalid MCP task handler version');
      if (!Array.isArray(inputMethods) || inputMethods.some(method => !SUPPORTED_INPUT_METHODS.has(method))) throw new Error('Invalid MCP task input capabilities');
      const taskID = randomUUID();
      const encryptedArgs = encryptJSON(args, payloadKeyring.active, ownerKeyID, taskAAD(namespace, taskID, 'arguments'), MAX_ARGUMENT_BYTES, 'MCP task arguments', payloadEncryptionVersion);
      const owners = ownerHashes(authInfo, authMode);
      const owner = owners[0];
      const ownerValues = owners.map(hash => hash.toString('hex'));
      const result = await withTransaction(pool, async client => {
        // Serialize admission across web replicas, using the same database clock
        // and transaction as the insert. Waiting for input still consumes capacity.
        await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1, 0))', [`${TABLE}:admission:${namespace}`]);
        const counts = await client.query(`
          SELECT COUNT(*)::int AS total, COUNT(*) FILTER (WHERE ${ownerHashPredicate(owners, 2)})::int AS owned
            FROM ${TABLE} WHERE namespace = $1 AND expires_at > clock_timestamp()
             AND status IN ('queued', 'running', 'input_required')
        `, [namespace, ...ownerValues]);
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
      return rowTask(result.rows[0], payloadKeyring, namespace);
    },
    async get({ taskID, authInfo, authMode }) {
      if (typeof taskID !== 'string' || !UUID_PATTERN.test(taskID)) return null;
      const owners = ownerHashes(authInfo, authMode);
      const result = await pool.query(`
        SELECT task_id::text AS task_id, tool_name, handler_version, status,
               created_at, updated_at, expires_at, attempt_count,
               lease_token, cancel_requested_at, result_encrypted, error_encrypted,
               input_state_encrypted, input_methods
          FROM ${TABLE}
         WHERE namespace = $1 AND task_id = $2::uuid AND ${ownerHashPredicate(owners, 3)}
           AND expires_at > clock_timestamp()
      `, [namespace, taskID, ...owners.map(hash => hash.toString('hex'))]);
      return result.rows?.[0] ? rowTask(result.rows[0], payloadKeyring, namespace) : null;
    },
    async getMany({ taskIDs, authInfo, authMode }) {
      const ids = [...new Set((Array.isArray(taskIDs) ? taskIDs : []).filter(taskID => typeof taskID === 'string' && UUID_PATTERN.test(taskID)))];
      if (ids.length === 0) return [];
      const owners = ownerHashes(authInfo, authMode);
      const result = await pool.query(`
        SELECT task_id::text AS task_id, tool_name, handler_version, status,
               created_at, updated_at, expires_at, attempt_count,
               lease_token, cancel_requested_at, result_encrypted, error_encrypted,
               input_state_encrypted, input_methods
          FROM ${TABLE}
         WHERE namespace = $1 AND task_id = ANY($2::uuid[]) AND ${ownerHashPredicate(owners, 3)}
           AND expires_at > clock_timestamp()
      `, [namespace, ids, ...owners.map(hash => hash.toString('hex'))]);
      return (result.rows || []).map(row => rowTask(row, payloadKeyring, namespace));
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

        const state = decryptInputState(selected.rows[0].input_state_encrypted, payloadKeyring, namespace, taskID);
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
          const encrypted = encryptJSON(state, payloadKeyring.active, ownerKeyID, taskAAD(namespace, taskID, 'input-state'), MAX_INPUT_STATE_BYTES, 'MCP task input state', payloadEncryptionVersion);
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
        const owners = ownerHashes(authInfo, authMode);
        const selected = await client.query(`
          SELECT status, input_state_encrypted
            FROM ${TABLE}
           WHERE namespace = $1 AND task_id = $2::uuid AND ${ownerHashPredicate(owners, 3)}
             AND expires_at > clock_timestamp()
           FOR UPDATE
        `, [namespace, taskID, ...owners.map(hash => hash.toString('hex'))]);
        const row = selected.rows?.[0];
        if (!row) return false;
        if (row.status !== 'input_required' || row.input_state_encrypted == null) return true;

        const state = decryptInputState(row.input_state_encrypted, payloadKeyring, namespace, taskID);
        let changed = false;
        for (const [key, response] of responseEntries) {
          if (!Object.hasOwn(state.requests, key) || Object.hasOwn(state.responses, key)) continue;
          state.responses[key] = response;
          changed = true;
        }
        if (!changed) return true;

        const pending = Object.keys(state.requests).some(key => !Object.hasOwn(state.responses, key));
        const encrypted = encryptJSON(state, payloadKeyring.active, ownerKeyID, taskAAD(namespace, taskID, 'input-state'), MAX_INPUT_STATE_BYTES, 'MCP task input state', payloadEncryptionVersion);
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
      const owners = ownerHashes(authInfo, authMode);
      const result = await pool.query(`
        UPDATE ${TABLE}
           SET status = CASE WHEN status IN ('queued', 'input_required') THEN 'cancelled' ELSE status END,
               cancel_requested_at = CASE WHEN status IN ('queued', 'running', 'input_required') THEN COALESCE(cancel_requested_at, clock_timestamp()) ELSE cancel_requested_at END,
               updated_at = CASE WHEN status IN ('queued', 'running', 'input_required') THEN clock_timestamp() ELSE updated_at END,
               lease_token = CASE WHEN status IN ('queued', 'input_required') THEN NULL ELSE lease_token END,
               lease_expires_at = CASE WHEN status IN ('queued', 'input_required') THEN NULL ELSE lease_expires_at END
         WHERE namespace = $1 AND task_id = $2::uuid AND ${ownerHashPredicate(owners, 3)}
           AND expires_at > clock_timestamp()
         RETURNING task_id
      `, [namespace, taskID, ...owners.map(hash => hash.toString('hex'))]);
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
      const result = await pool.query(`
        WITH candidate AS (
          SELECT namespace, task_id
            FROM ${TABLE}
           WHERE namespace = $1 AND expires_at > clock_timestamp()
             AND (attempt_count < $2 OR (status = 'queued' AND resume_pending AND attempt_count = $2))
             AND ($5::jsonb IS NULL OR EXISTS (
               SELECT 1 FROM jsonb_to_recordset($5::jsonb) AS handler(name text, version text)
                WHERE handler.name = tool_name AND handler.version = handler_version
             ))
               AND (status = 'queued' OR (status = 'running' AND lease_expires_at <= clock_timestamp()))
           ORDER BY created_at, task_id
           FOR UPDATE SKIP LOCKED
           LIMIT 1
        )
        UPDATE ${TABLE} AS task
           SET status = 'running',
               attempt_count = task.attempt_count + CASE WHEN task.resume_pending THEN 0 ELSE 1 END,
               resume_pending = false,
               lease_token = $3::uuid,
               lease_expires_at = clock_timestamp() + ($4::bigint * interval '1 millisecond'),
               updated_at = clock_timestamp()
          FROM candidate
         WHERE task.namespace = candidate.namespace AND task.task_id = candidate.task_id
         RETURNING task.task_id::text AS task_id, task.tool_name, task.handler_version,
                   task.status, task.created_at, task.updated_at, task.expires_at,
                   task.attempt_count, task.lease_token::text AS lease_token,
                   task.cancel_requested_at, task.arguments_encrypted,
                   task.result_encrypted, task.error_encrypted,
                   task.input_state_encrypted, task.input_methods
      `, [namespace, maxAttempts, leaseToken, leaseMs, supportedHandlers == null ? null : JSON.stringify(supportedHandlers)]);
      return result.rows?.[0] ? rowTask(result.rows[0], payloadKeyring, namespace, true) : null;
    },
    async releaseMany(tasks) {
      if (!tasks.length) return;
      await pool.query({ text: `UPDATE ${TABLE} AS task
        SET status=CASE WHEN task.cancel_requested_at IS NULL THEN 'queued' ELSE 'cancelled' END,
            resume_pending=task.cancel_requested_at IS NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=clock_timestamp()
        FROM unnest($2::uuid[], $3::uuid[]) AS owner(task_id,lease_token)
        WHERE task.namespace=$1 AND task.task_id=owner.task_id AND task.lease_token=owner.lease_token AND task.status='running'`,
        values: [namespace, tasks.map(task => task.task_id), tasks.map(task => task.lease_token)], query_timeout: 5000 });
    },
    async release(taskID, leaseToken) {
      const result = await pool.query(`UPDATE ${TABLE}
        SET status=CASE WHEN cancel_requested_at IS NULL THEN 'queued' ELSE 'cancelled' END,
            resume_pending=cancel_requested_at IS NULL, lease_token=NULL, lease_expires_at=NULL,
            updated_at=clock_timestamp()
        WHERE namespace=$1 AND task_id=$2::uuid AND lease_token=$3::uuid AND status='running'
        RETURNING status`, [namespace, taskID, leaseToken]);
      return result.rows?.[0]?.status ?? null;
    },
    async heartbeat(taskID, leaseToken, leaseMs) {
      const result = await pool.query(`
        UPDATE ${TABLE}
           SET lease_expires_at = clock_timestamp() + ($4::bigint * interval '1 millisecond')
         WHERE namespace = $1 AND task_id = $2::uuid AND lease_token = $3::uuid AND status = 'running'
         RETURNING cancel_requested_at
      `, [namespace, taskID, leaseToken, leaseMs]);
      if (!result.rows?.[0]) return { owned: false, cancelRequested: false };
      return { owned: true, cancelRequested: result.rows[0].cancel_requested_at != null };
    },
    async complete(taskID, leaseToken, value) {
      const encrypted = encryptJSON(value, payloadKeyring.active, ownerKeyID, taskAAD(namespace, taskID, 'result'), MAX_RESULT_BYTES, 'MCP task result', payloadEncryptionVersion);
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
    async fail(taskID, leaseToken, error) {
      const encrypted = encryptJSON(error, payloadKeyring.active, ownerKeyID, taskAAD(namespace, taskID, 'error'), MAX_RESULT_BYTES, 'MCP task error', payloadEncryptionVersion);
      const result = await pool.query(`
        UPDATE ${TABLE}
           SET status = CASE WHEN cancel_requested_at IS NULL THEN 'failed' ELSE 'cancelled' END,
               error_encrypted = CASE WHEN cancel_requested_at IS NULL THEN $4::bytea ELSE NULL END,
               result_encrypted = NULL, updated_at = clock_timestamp(),
               lease_token = NULL, lease_expires_at = NULL
         WHERE namespace = $1 AND task_id = $2::uuid AND lease_token = $3::uuid AND status = 'running'
         RETURNING status
      `, [namespace, taskID, leaseToken, encrypted]);
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
      await pool.query(`
        WITH expired AS (
          SELECT namespace, task_id FROM ${TABLE}
           WHERE namespace = $1 AND expires_at <= clock_timestamp()
           ORDER BY expires_at LIMIT $2
        )
        DELETE FROM ${TABLE} AS task
         USING expired
         WHERE task.namespace = expired.namespace AND task.task_id = expired.task_id
      `, [namespace, limit]);
    },
    async rewrapBatch(limit = 32) {
      if (payloadEncryptionVersion !== 2) return 0;
      if (!Number.isSafeInteger(limit) || limit < 1 || limit > 256) throw new Error('MCP task rewrap batch size is invalid');
      const needsRewrap = [
        'arguments_encrypted', 'result_encrypted', 'error_encrypted', 'input_state_encrypted',
      ].map(needsPayloadRewrapSQL).join(' OR ');
      return withTransaction(pool, async client => {
        await client.query("SELECT set_config('gregale.mcp_task_rewrap', 'on', true)");
        const selected = await client.query(`
          SELECT task_id::text AS task_id, arguments_encrypted, result_encrypted,
                 error_encrypted, input_state_encrypted
            FROM ${TABLE}
           WHERE namespace = $1 AND expires_at > clock_timestamp()
             AND (${needsRewrap})
           ORDER BY created_at LIMIT $3 FOR UPDATE SKIP LOCKED
        `, [namespace, Buffer.from(ownerKeyID, 'ascii'), limit]);
        for (const row of selected.rows || []) {
          const rotate = (value, field, maxBytes, label) => {
            if (value == null) return null;
            const aad = taskAAD(namespace, row.task_id, field);
            const plaintext = decryptPayload(value, payloadKeyring, aad);
            if (plaintext.length > maxBytes) throw new Error(`${label} exceeds the ${maxBytes}-byte MCP task limit`);
            return encryptPayload(plaintext, payloadKeyring.active, ownerKeyID, aad, 2);
          };
          const args = rotate(row.arguments_encrypted, 'arguments', MAX_ARGUMENT_BYTES, 'MCP task arguments');
          const result = rotate(row.result_encrypted, 'result', MAX_RESULT_BYTES, 'MCP task result');
          const error = rotate(row.error_encrypted, 'error', MAX_RESULT_BYTES, 'MCP task error');
          const inputState = rotate(row.input_state_encrypted, 'input-state', MAX_INPUT_STATE_BYTES, 'MCP task input state');
          await client.query(`
            UPDATE ${TABLE}
               SET arguments_encrypted = $3, result_encrypted = $4,
                   error_encrypted = $5, input_state_encrypted = $6
             WHERE namespace = $1 AND task_id = $2::uuid
          `, [namespace, row.task_id, args, result, error, inputState]);
        }
        return selected.rows?.length ?? 0;
      });
    },
  };
}

// Read-only observers never initialize schema, decrypt payloads, or claim work.
function createQueueMetricsObserver({ pool, namespace, verifyReadOnly }) {
  async function assertReadOnly() {
    const result = await pool.query(`
      SELECT pg_catalog.to_regclass($1)::oid IS NOT NULL AS table_exists,
             COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'INSERT') OR
                      pg_catalog.has_any_column_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'INSERT'), false) AS can_insert,
             COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'UPDATE') OR
                      pg_catalog.has_any_column_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'UPDATE'), false) AS can_update,
             COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'DELETE'), false) AS can_delete,
             COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'TRUNCATE'), false) AS can_truncate,
             COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'REFERENCES') OR
                      pg_catalog.has_any_column_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'REFERENCES'), false) AS can_reference,
             COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'TRIGGER'), false) AS can_create_trigger,
             pg_catalog.has_database_privilege(current_user, current_database(), 'CREATE') AS can_create_schemas,
             COALESCE((
               SELECT pg_catalog.has_schema_privilege(current_user, n.nspname, 'CREATE')
                 FROM pg_catalog.pg_class AS c
                 JOIN pg_catalog.pg_namespace AS n ON n.oid = c.relnamespace
                WHERE c.oid = pg_catalog.to_regclass($1)::oid
             ), false) AS can_create_in_task_schema
    `, [TABLE]);
    const row = result.rows?.[0];
    if (!row || row.table_exists !== true || [
      row.can_insert,
      row.can_update,
      row.can_delete,
      row.can_truncate,
      row.can_reference,
      row.can_create_trigger,
      row.can_create_schemas,
      row.can_create_in_task_schema,
    ].some(Boolean)) {
      throw new Error('MCP task observer requires read-only PostgreSQL access');
    }
    const capabilityResult = await pool.query(`
      SELECT pg_catalog.to_regclass($1)::oid IS NOT NULL AS table_exists,
             COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'INSERT') OR
                      pg_catalog.has_any_column_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'INSERT'), false) AS can_insert,
             COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'UPDATE') OR
                      pg_catalog.has_any_column_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'UPDATE'), false) AS can_update,
             COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'DELETE'), false) AS can_delete,
             COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'TRUNCATE'), false) AS can_truncate,
             COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'REFERENCES') OR
                      pg_catalog.has_any_column_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'REFERENCES'), false) AS can_reference,
             COALESCE(pg_catalog.has_table_privilege(current_user, pg_catalog.to_regclass($1)::oid, 'TRIGGER'), false) AS can_create_trigger,
             COALESCE((SELECT pg_catalog.has_schema_privilege(current_user, n.nspname, 'CREATE')
                         FROM pg_catalog.pg_class AS c JOIN pg_catalog.pg_namespace AS n ON n.oid=c.relnamespace
                        WHERE c.oid=pg_catalog.to_regclass($1)::oid), false) AS can_create_in_task_schema
    `, [WORKER_HANDLERS_TABLE]);
    const capabilityRow = capabilityResult.rows?.[0];
    if (!capabilityRow || capabilityRow.table_exists !== true || [
      capabilityRow.can_insert, capabilityRow.can_update, capabilityRow.can_delete,
      capabilityRow.can_truncate, capabilityRow.can_reference, capabilityRow.can_create_trigger,
      capabilityRow.can_create_in_task_schema,
    ].some(Boolean)) {
      throw new Error('MCP task observer requires read-only PostgreSQL access');
    }
  }

  async function queueMetrics() {
    if (verifyReadOnly) await assertReadOnly();
    const result = await pool.query(`
      SELECT LEAST(COUNT(*), 1000000000000::bigint)::text AS outstanding_tasks,
             LEAST(GREATEST(EXTRACT(EPOCH FROM (clock_timestamp() - MIN(created_at))), 0), 1000000000000)::text AS oldest_age_seconds,
             (SELECT LEAST(COUNT(*), 1000000000000::bigint)::text
                FROM ${TABLE} AS queued
               WHERE queued.namespace=$1 AND queued.expires_at>clock_timestamp()
                 AND (queued.status='queued' OR (queued.status='running' AND queued.lease_expires_at<=clock_timestamp()))
                 AND (queued.attempt_count<3 OR (queued.status='queued' AND queued.resume_pending AND queued.attempt_count=3))
                 AND NOT EXISTS (
                   SELECT 1 FROM ${WORKER_HANDLERS_TABLE} AS handler
                    WHERE handler.namespace=queued.namespace AND handler.tool_name=queued.tool_name
                      AND handler.handler_version=queued.handler_version
                      AND handler.heartbeat_at>clock_timestamp()-interval '45 seconds'
                 )) AS without_compatible_worker_tasks
        FROM ${TABLE}
       WHERE namespace = $1
         AND expires_at > clock_timestamp()
         AND status IN ('queued', 'running')
    `, [namespace]);
    const row = result.rows?.[0];
    if (!row) throw new Error('Could not read MCP task queue metrics');
    const outstandingTasks = Number(row.outstanding_tasks);
    const oldestAgeSeconds = Number(row.oldest_age_seconds);
    const withoutCompatibleWorkerTasks = Number(row.without_compatible_worker_tasks);
    if (!Number.isSafeInteger(outstandingTasks) || outstandingTasks < 0 || !Number.isFinite(oldestAgeSeconds) || oldestAgeSeconds < 0 ||
        !Number.isSafeInteger(withoutCompatibleWorkerTasks) || withoutCompatibleWorkerTasks < 0) {
      throw new Error('MCP task queue metrics returned invalid values');
    }
    let handlerCompatibility = [];
    let compatibilityTruncated = false;
    if (verifyReadOnly) {
      let compatibilityResult;
      try {
        compatibilityResult = await pool.query(`
        WITH pending AS (
          SELECT queued.namespace, queued.tool_name, queued.handler_version
            FROM ${TABLE} AS queued
           WHERE queued.namespace=$1 AND queued.expires_at>clock_timestamp()
             AND queued.status IN ('queued','running','input_required')
        ), waiting AS (
          SELECT tool_name, handler_version, COUNT(*)::bigint AS waiting_tasks
            FROM pending
           GROUP BY tool_name, handler_version
        ), limited_waiting AS (
          SELECT *, COUNT(*) OVER () AS queue_pair_count
            FROM waiting
           ORDER BY waiting_tasks DESC, tool_name, handler_version
           LIMIT 257
        ), by_deployment AS (
          SELECT waiting.tool_name, waiting.handler_version, waiting.waiting_tasks, waiting.queue_pair_count,
                 handler.deployment_id, COUNT(DISTINCT handler.worker_id)::bigint AS workers
            FROM limited_waiting AS waiting
            LEFT JOIN ${WORKER_HANDLERS_TABLE} AS handler
              ON handler.namespace=$1 AND handler.tool_name=waiting.tool_name
             AND handler.handler_version=waiting.handler_version
             AND handler.heartbeat_at>clock_timestamp()-interval '45 seconds'
           GROUP BY waiting.tool_name, waiting.handler_version, waiting.waiting_tasks,
                    waiting.queue_pair_count, handler.deployment_id
        ), ranked AS (
          SELECT *,
                 SUM(workers) OVER (PARTITION BY tool_name,handler_version) AS all_workers,
                 COUNT(*) OVER (PARTITION BY tool_name,handler_version) AS deployment_count,
                 ROW_NUMBER() OVER (PARTITION BY tool_name,handler_version ORDER BY workers DESC,deployment_id) AS deployment_rank
            FROM by_deployment
        )
        SELECT tool_name,handler_version,
               LEAST(waiting_tasks,1000000000000::bigint)::text AS waiting_tasks,
               LEAST(all_workers,1000000000000::bigint)::text AS live_compatible_workers,
               COALESCE(deployment_id,'') AS deployment_id,
               LEAST(workers,1000000000000::bigint)::text AS deployment_workers,
               deployment_count, queue_pair_count
          FROM ranked
         WHERE deployment_rank<=64
         ORDER BY waiting_tasks DESC,tool_name,handler_version,deployment_rank
      `, [namespace]);
      } catch (error) {
        if (error?.code !== '42703') throw error;
        // The read-only observer cannot migrate an old worker table. Keep
        // queue metrics available and publish a fail-closed compatibility
        // snapshot until a worker adds deployment identity.
        compatibilityTruncated = true;
        return { outstandingTasks, oldestAgeSeconds, withoutCompatibleWorkerTasks, handlerCompatibility, compatibilityTruncated };
      }
      const rows = compatibilityResult.rows ?? [];
      const byHandler = new Map();
      let deploymentEntries = 0;
      for (const row of rows) {
        const key = JSON.stringify([row.tool_name, row.handler_version]);
        let item = byHandler.get(key);
        if (!item) {
          item = {
            tool_name: row.tool_name,
            handler_version: row.handler_version,
            waiting_tasks: Number(row.waiting_tasks),
            live_compatible_workers: Number(row.live_compatible_workers),
            unknown_deployment_workers: Number(row.live_compatible_workers),
            deployments: [],
          };
          byHandler.set(key, item);
        }
        if (Number(row.queue_pair_count) > 256 || Number(row.deployment_count) > 64) compatibilityTruncated = true;
        const workers = Number(row.deployment_workers);
        if (row.deployment_id && workers > 0 && deploymentEntries < 768) {
          item.deployments.push({ deployment_id: row.deployment_id, workers });
          item.unknown_deployment_workers -= workers;
          deploymentEntries += 1;
        } else if (row.deployment_id && workers > 0) {
          compatibilityTruncated = true;
        }
      }
      handlerCompatibility = [...byHandler.values()];
      if (handlerCompatibility.length > 256) {
        compatibilityTruncated = true;
        handlerCompatibility = handlerCompatibility.slice(0, 256);
      }
    }
    return { outstandingTasks, oldestAgeSeconds, withoutCompatibleWorkerTasks, handlerCompatibility, compatibilityTruncated };
  }
  return { queueMetrics };
}

export function createMcpTaskQueueObserver({ pool, namespace }) {
  return createQueueMetricsObserver({ pool, namespace, verifyReadOnly: true });
}
