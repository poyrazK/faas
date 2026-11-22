// The thread store: history and seen-message IDs in your Postgres
// (ADR-20261030125501 §5), bound to the app as DATABASE_URL.
//
// Each slice opens one connection and closes it before returning. Nothing
// is kept open between messages, so a parked agent holds no connection and
// a VM snapshot never captures a socket.
import pg from "pg";
import { agentSchema } from "./schema.js";

// schemaLock serializes schema creation. A new database's first slices run
// in parallel (the trigger's max_concurrency), and concurrent CREATE TABLE IF
// NOT EXISTS of one table collide in Postgres's catalog ("duplicate key value
// violates unique constraint pg_type_typname_nsp_index"): the slice fails and
// is retried. The schema runs as one simple query, which Postgres executes
// as one implicit transaction, so the transaction-scoped lock covers every
// statement. The key differs from the agent-infinity-rust template's.
export const schemaLock = "0x6167656e74730002";

export const schema = `
SELECT pg_advisory_xact_lock(${schemaLock});
${agentSchema}`;

export class PostgresStore {
  constructor(connectionString = process.env.DATABASE_URL, { newClient } = {}) {
    if (!connectionString) throw new Error("DATABASE_URL is not set; attach a Postgres database to this app");
    this.connectionString = connectionString;
    this.newClient = newClient ?? ((connectionString) => new pg.Client({ connectionString, connectionTimeoutMillis: 5_000 }));
    this.ready = false;
  }

  async withClient(fn) {
    const client = this.newClient(this.connectionString);
    await client.connect();
    try {
      if (!this.ready) {
        await client.query(schema);
        this.ready = true;
      }
      return await fn(client);
    } finally {
      await client.end();
    }
  }

  // transaction runs fn with the thread locked. The consumer group already
  // delivers a thread's messages one at a time; the lock also covers a
  // reader or a replay running beside it.
  transaction(threadID, fn) {
    return this.withClient(async (client) => {
      await client.query("BEGIN");
      try {
        await client.query("SELECT pg_advisory_xact_lock(hashtext($1))", [threadID]);
        const out = await fn(threadTx(client, threadID));
        await client.query("COMMIT");
        return out;
      } catch (err) {
        await client.query("ROLLBACK").catch(() => {});
        throw err;
      }
    });
  }

  markDispatched(threadID, messageID) {
    return this.withClient((client) => client.query(
      `UPDATE agent_messages SET content = jsonb_set(content, '{tool_call,dispatched}', 'true')
        WHERE thread_id = $1 AND message_id = $2`, [threadID, messageID]));
  }

  thread(threadID) {
    return this.withClient(async (client) => {
      const { rows } = await client.query(
        "SELECT message_id, role, content, created_at FROM agent_messages WHERE thread_id = $1 ORDER BY seq", [threadID]);
      return rows;
    });
  }
}

function threadTx(client, threadID) {
  return {
    async seen(messageID) {
      const { rowCount } = await client.query(
        "SELECT 1 FROM agent_messages WHERE thread_id = $1 AND message_id = $2", [threadID, messageID]);
      return rowCount > 0;
    },
    async undispatched(messageID) {
      const { rows } = await client.query(
        `SELECT content->'tool_call' AS call FROM agent_messages
          WHERE thread_id = $1 AND message_id = $2 AND (content->'tool_call'->>'dispatched')::boolean IS FALSE`,
        [threadID, `assistant:${messageID}`]);
      return rows[0]?.call ?? null;
    },
    async answered(messageID) {
      const { rowCount } = await client.query(
        `SELECT 1 FROM agent_messages
          WHERE thread_id = $1 AND message_id = $2 AND (content->>'final')::boolean IS TRUE`,
        [threadID, `assistant:${messageID}`]);
      return rowCount > 0;
    },
    async history() {
      const { rows } = await client.query(
        "SELECT role, content FROM agent_messages WHERE thread_id = $1 ORDER BY seq", [threadID]);
      return rows;
    },
    async append(messageID, role, content) {
      await client.query(
        "INSERT INTO agent_messages (thread_id, message_id, role, content) VALUES ($1, $2, $3, $4)",
        [threadID, messageID, role, JSON.stringify(content)]);
    },
  };
}
