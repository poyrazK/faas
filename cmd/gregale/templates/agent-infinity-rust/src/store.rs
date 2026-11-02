// Infinity's two stores on plain Postgres (ADR-20261030125501 §5): the
// conversation store (history, the thread tree, compaction summaries) and
// the state store (processed message IDs, metadata, subscriptions, pending
// choices). The data stays in your database, under your retention.
//
// A slice opens one connection and drops it when it returns, so a parked
// agent holds no connection and no snapshot captures a socket. Gregale's
// consumer group runs one slice per thread at a time, which is the
// serialization Infinity's step mode requires.

use std::collections::HashSet;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

use async_trait::async_trait;
use infinity_agent_core::ThreadId;
use infinity_agent_core::message::InfinityMessage;
use infinity_agent_core::system::UserChoice;
use infinity_agent_core::traits::{ConversationStore, SpawnContext, StateStore};
use rap_protocol::{ChoiceId, ToolCallId};
use tokio_postgres::Client;

const SCHEMA: &str = r#"
CREATE TABLE IF NOT EXISTS infinity_threads (
    thread_id text PRIMARY KEY,
    parent_thread_id text,
    root_thread_id text NOT NULL,
    spawn_message_order bigint,
    spawn_tool_call_id text,
    closed boolean NOT NULL DEFAULT false,
    is_subscription_event boolean NOT NULL DEFAULT false,
    is_compaction boolean NOT NULL DEFAULT false,
    fresh_context boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS infinity_messages (
    thread_id text NOT NULL,
    message_order bigint NOT NULL,
    message_id text NOT NULL,
    message jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (thread_id, message_order)
);
CREATE TABLE IF NOT EXISTS infinity_compactions (
    seq bigserial PRIMARY KEY,
    thread_id text NOT NULL,
    up_to_order bigint NOT NULL,
    summary text NOT NULL
);
CREATE INDEX IF NOT EXISTS infinity_compactions_thread ON infinity_compactions (thread_id, seq);
CREATE TABLE IF NOT EXISTS infinity_processed_ids (
    thread_id text NOT NULL,
    message_id text NOT NULL,
    PRIMARY KEY (thread_id, message_id)
);
CREATE TABLE IF NOT EXISTS infinity_metadata (
    root_thread_id text PRIMARY KEY,
    metadata jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS infinity_subscriptions (
    thread_id text NOT NULL,
    tool_call_id text NOT NULL,
    PRIMARY KEY (thread_id, tool_call_id)
);
CREATE TABLE IF NOT EXISTS infinity_user_choices (
    seq bigserial,
    thread_id text NOT NULL,
    choice_id text NOT NULL,
    choice jsonb NOT NULL,
    PRIMARY KEY (thread_id, choice_id)
);
CREATE TABLE IF NOT EXISTS agent_held_tool_calls (
    thread_id text NOT NULL,
    tool_call_id text NOT NULL,
    invocation jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (thread_id, tool_call_id)
);
"#;

static SCHEMA_READY: AtomicBool = AtomicBool::new(false);

#[derive(Debug)]
pub struct StoreError(pub String);

impl std::fmt::Display for StoreError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "postgres store: {}", self.0)
    }
}

impl std::error::Error for StoreError {}

impl From<tokio_postgres::Error> for StoreError {
    fn from(e: tokio_postgres::Error) -> Self {
        StoreError(e.to_string())
    }
}

impl From<serde_json::Error> for StoreError {
    fn from(e: serde_json::Error) -> Self {
        StoreError(e.to_string())
    }
}

/// One slice's connection, shared by both stores and the held tool calls.
#[derive(Clone)]
pub struct Db(pub Arc<Client>);

impl Db {
    /// Create the tables once per process; every statement is idempotent.
    pub async fn ensure_schema(&self) -> Result<(), StoreError> {
        if SCHEMA_READY.load(Ordering::Acquire) {
            return Ok(());
        }
        self.0.batch_execute(SCHEMA).await?;
        SCHEMA_READY.store(true, Ordering::Release);
        Ok(())
    }

    /// Record a tool call that no tool server takes, so your backend can read
    /// it from GET /threads/:id and post the result to its callback URL.
    pub async fn hold_tool_call(
        &self,
        thread: &str,
        id: &str,
        invocation: &serde_json::Value,
    ) -> Result<(), StoreError> {
        self.0
            .execute(
                "INSERT INTO agent_held_tool_calls (thread_id, tool_call_id, invocation) VALUES ($1, $2, $3)
                 ON CONFLICT (thread_id, tool_call_id) DO UPDATE SET invocation = EXCLUDED.invocation",
                &[&thread, &id, invocation],
            )
            .await?;
        Ok(())
    }

    /// The thread as GET /threads/:id shows it: its history in Infinity's
    /// own format, and the held tool calls that have no result yet.
    pub async fn thread_view(&self, thread: &str) -> Result<serde_json::Value, StoreError> {
        let messages: Vec<serde_json::Value> = self
            .0
            .query(
                "SELECT message FROM infinity_messages WHERE thread_id = $1 ORDER BY message_order",
                &[&thread],
            )
            .await?
            .iter()
            .map(|row| row.get(0))
            .collect();
        let held: Vec<serde_json::Value> = self
            .0
            .query(
                "SELECT h.invocation FROM agent_held_tool_calls h WHERE h.thread_id = $1 AND NOT EXISTS (
                    SELECT 1 FROM infinity_messages m WHERE m.thread_id = h.thread_id
                    AND m.message->>'kind' = 'tool_result' AND m.message->'result'->>'id' = h.tool_call_id)
                 ORDER BY h.created_at",
                &[&thread],
            )
            .await?
            .iter()
            .map(|row| row.get(0))
            .collect();
        Ok(
            serde_json::json!({ "thread_id": thread, "messages": messages, "held_tool_calls": held }),
        )
    }
}

/// Infinity's ConversationStore on Postgres. Message orders are one-based and
/// contiguous per thread, so the trait's leading-row counts map to
/// `message_order > start_from AND message_order <= up_to`.
#[derive(Clone)]
pub struct PgConversationStore(pub Db);

/// Infinity's StateStore on Postgres.
#[derive(Clone)]
pub struct PgStateStore(pub Db);

impl PgConversationStore {
    fn client(&self) -> &Client {
        &self.0.0
    }
}

impl PgStateStore {
    fn client(&self) -> &Client {
        &self.0.0
    }
}

fn bool_column(rows: &[tokio_postgres::Row]) -> bool {
    rows.first()
        .map(|row| row.get::<_, bool>(0))
        .unwrap_or(false)
}

#[async_trait]
impl ConversationStore for PgConversationStore {
    type Error = StoreError;

    async fn ensure_root_thread(&self, thread_id: &ThreadId<str>) -> Result<(), StoreError> {
        self.client().execute(
                "INSERT INTO infinity_threads (thread_id, root_thread_id) VALUES ($1, $1) ON CONFLICT (thread_id) DO NOTHING",
                &[&thread_id.as_str()],
            )
            .await?;
        Ok(())
    }

    async fn thread_exists(&self, thread_id: &ThreadId<str>) -> Result<bool, StoreError> {
        let row = self
            .client()
            .query_one(
                "SELECT EXISTS (SELECT 1 FROM infinity_threads WHERE thread_id = $1)",
                &[&thread_id.as_str()],
            )
            .await?;
        Ok(row.get(0))
    }

    async fn load_history_up_to(
        &self,
        session_id: &ThreadId<str>,
        start_from: Option<i64>,
        up_to: Option<i64>,
    ) -> Result<Vec<InfinityMessage>, StoreError> {
        let rows = self.client().query(
                "SELECT message FROM infinity_messages WHERE thread_id = $1
                 AND ($2::bigint IS NULL OR message_order > $2) AND ($3::bigint IS NULL OR message_order <= $3)
                 ORDER BY message_order",
                &[&session_id.as_str(), &start_from, &up_to],
            )
            .await?;
        rows.iter()
            .map(|row| Ok(serde_json::from_value(row.get::<_, serde_json::Value>(0))?))
            .collect()
    }

    async fn append_messages(
        &self,
        session_id: &ThreadId<str>,
        messages: Vec<(InfinityMessage, String)>,
    ) -> Result<(), StoreError> {
        if messages.is_empty() {
            return Ok(());
        }
        let mut ids = Vec::with_capacity(messages.len());
        let mut bodies = Vec::with_capacity(messages.len());
        for (message, id) in messages {
            ids.push(id);
            bodies.push(serde_json::to_value(&message)?);
        }
        // One statement numbers and inserts the batch, so a failure leaves no
        // gap in the thread's orders.
        self.client().execute(
                "INSERT INTO infinity_messages (thread_id, message_order, message_id, message)
                 SELECT $1, base.n + m.ord, m.id, m.body
                 FROM (SELECT COALESCE(MAX(message_order), 0) AS n FROM infinity_messages WHERE thread_id = $1) base,
                      unnest($2::text[], $3::jsonb[]) WITH ORDINALITY AS m(id, body, ord)",
                &[&session_id.as_str(), &ids, &bodies],
            )
            .await?;
        Ok(())
    }

    async fn spawn_thread(
        &self,
        parent_thread_id: &ThreadId<str>,
        spawn_tool_call_id: &ToolCallId<str>,
        is_for_subscription_event: bool,
        context: SpawnContext,
    ) -> Result<ThreadId, StoreError> {
        let id = uuid::Uuid::new_v4().to_string();
        let (order, fresh) = match context {
            SpawnContext::InheritUpTo(order) => (order as i64, false),
            SpawnContext::Fresh => (0, true),
        };
        self.client().execute(
                "INSERT INTO infinity_threads (thread_id, parent_thread_id, root_thread_id, spawn_message_order,
                    spawn_tool_call_id, is_subscription_event, fresh_context)
                 SELECT $1, $2, COALESCE((SELECT root_thread_id FROM infinity_threads WHERE thread_id = $2), $2), $3, $4, $5, $6",
                &[&id, &parent_thread_id.as_str(), &order, &spawn_tool_call_id.as_str(), &is_for_subscription_event, &fresh],
            )
            .await?;
        Ok(ThreadId::from(id))
    }

    async fn is_thread_closed(&self, thread_id: &ThreadId<str>) -> Result<bool, StoreError> {
        let rows = self
            .client()
            .query(
                "SELECT closed FROM infinity_threads WHERE thread_id = $1",
                &[&thread_id.as_str()],
            )
            .await?;
        Ok(bool_column(&rows))
    }

    async fn close_thread(&self, thread_id: &ThreadId<str>) -> Result<(), StoreError> {
        self.client()
            .execute(
                "UPDATE infinity_threads SET closed = true WHERE thread_id = $1",
                &[&thread_id.as_str()],
            )
            .await?;
        Ok(())
    }

    async fn is_subscription_event_thread(
        &self,
        thread_id: &ThreadId<str>,
    ) -> Result<bool, StoreError> {
        let rows = self
            .client()
            .query(
                "SELECT is_subscription_event FROM infinity_threads WHERE thread_id = $1",
                &[&thread_id.as_str()],
            )
            .await?;
        Ok(bool_column(&rows))
    }

    async fn is_fresh_context_thread(&self, thread_id: &ThreadId<str>) -> Result<bool, StoreError> {
        let rows = self
            .client()
            .query(
                "SELECT fresh_context FROM infinity_threads WHERE thread_id = $1",
                &[&thread_id.as_str()],
            )
            .await?;
        Ok(bool_column(&rows))
    }

    async fn get_thread_parent_info(
        &self,
        thread_id: &ThreadId<str>,
    ) -> Result<Option<(ThreadId, ToolCallId)>, StoreError> {
        let rows = self.client().query(
                "SELECT parent_thread_id, spawn_tool_call_id FROM infinity_threads WHERE thread_id = $1",
                &[&thread_id.as_str()],
            )
            .await?;
        Ok(rows.first().and_then(|row| {
            let parent: Option<String> = row.get(0);
            let call: Option<String> = row.get(1);
            Some((ThreadId::from(parent?), ToolCallId::from(call?)))
        }))
    }

    async fn get_ancestor_chain(
        &self,
        thread_id: &ThreadId<str>,
    ) -> Result<Vec<(ThreadId, i64)>, StoreError> {
        // Root first: each row is (parent, how much of the parent the child
        // inherits), from the root's child down to thread_id.
        let rows = self
            .client()
            .query(
                "WITH RECURSIVE chain AS (
                    SELECT thread_id, parent_thread_id, spawn_message_order, 0 AS depth
                    FROM infinity_threads WHERE thread_id = $1
                    UNION ALL
                    SELECT t.thread_id, t.parent_thread_id, t.spawn_message_order, c.depth + 1
                    FROM infinity_threads t JOIN chain c ON t.thread_id = c.parent_thread_id
                    WHERE c.depth < 1000
                 )
                 SELECT parent_thread_id, COALESCE(spawn_message_order, 0) FROM chain
                 WHERE parent_thread_id IS NOT NULL ORDER BY depth DESC",
                &[&thread_id.as_str()],
            )
            .await?;
        Ok(rows
            .iter()
            .map(|row| {
                (
                    ThreadId::from(row.get::<_, String>(0)),
                    row.get::<_, i64>(1),
                )
            })
            .collect())
    }

    async fn mark_thread_as_compaction(&self, thread_id: &ThreadId<str>) -> Result<(), StoreError> {
        self.client()
            .execute(
                "UPDATE infinity_threads SET is_compaction = true WHERE thread_id = $1",
                &[&thread_id.as_str()],
            )
            .await?;
        Ok(())
    }

    async fn is_compaction_thread(&self, thread_id: &ThreadId<str>) -> Result<bool, StoreError> {
        let rows = self
            .client()
            .query(
                "SELECT is_compaction FROM infinity_threads WHERE thread_id = $1",
                &[&thread_id.as_str()],
            )
            .await?;
        Ok(bool_column(&rows))
    }

    async fn get_thread_spawn_order(
        &self,
        thread_id: &ThreadId<str>,
    ) -> Result<Option<i64>, StoreError> {
        let rows = self
            .client()
            .query(
                "SELECT spawn_message_order FROM infinity_threads WHERE thread_id = $1",
                &[&thread_id.as_str()],
            )
            .await?;
        Ok(rows.first().and_then(|row| row.get::<_, Option<i64>>(0)))
    }

    async fn save_compaction_summary(
        &self,
        thread_id: &ThreadId<str>,
        summary: &str,
        up_to_order: i64,
    ) -> Result<(), StoreError> {
        self.client().execute(
                "INSERT INTO infinity_compactions (thread_id, up_to_order, summary) VALUES ($1, $2, $3)",
                &[&thread_id.as_str(), &up_to_order, &summary],
            )
            .await?;
        Ok(())
    }

    async fn load_latest_compaction_summary_up_to(
        &self,
        thread_id: &ThreadId<str>,
        up_to_order: Option<i64>,
    ) -> Result<Option<(String, i64)>, StoreError> {
        // The latest saved summary within the bound, as the in-memory store
        // picks it.
        let rows = self.client().query(
                "SELECT summary, up_to_order FROM infinity_compactions
                 WHERE thread_id = $1 AND ($2::bigint IS NULL OR up_to_order <= $2) ORDER BY seq DESC LIMIT 1",
                &[&thread_id.as_str(), &up_to_order],
            )
            .await?;
        Ok(rows.first().map(|row| (row.get(0), row.get(1))))
    }
}

#[async_trait]
impl StateStore for PgStateStore {
    type Error = StoreError;

    async fn get_processed_ids(
        &self,
        thread_id: &ThreadId<str>,
    ) -> Result<HashSet<String>, StoreError> {
        let rows = self
            .client()
            .query(
                "SELECT message_id FROM infinity_processed_ids WHERE thread_id = $1",
                &[&thread_id.as_str()],
            )
            .await?;
        Ok(rows.iter().map(|row| row.get(0)).collect())
    }

    async fn add_processed_message_ids(
        &self,
        thread_id: &ThreadId<str>,
        message_ids: Vec<String>,
    ) -> Result<(), StoreError> {
        self.client().execute(
                "INSERT INTO infinity_processed_ids (thread_id, message_id) SELECT $1, unnest($2::text[]) ON CONFLICT DO NOTHING",
                &[&thread_id.as_str(), &message_ids],
            )
            .await?;
        Ok(())
    }

    async fn get_metadata(
        &self,
        root_thread_id: &ThreadId<str>,
    ) -> Result<Option<serde_json::Value>, StoreError> {
        let rows = self
            .client()
            .query(
                "SELECT metadata FROM infinity_metadata WHERE root_thread_id = $1",
                &[&root_thread_id.as_str()],
            )
            .await?;
        Ok(rows.first().map(|row| row.get(0)))
    }

    async fn set_metadata(
        &self,
        root_thread_id: &ThreadId<str>,
        metadata: serde_json::Value,
    ) -> Result<(), StoreError> {
        self.client()
            .execute(
                "INSERT INTO infinity_metadata (root_thread_id, metadata) VALUES ($1, $2)
                 ON CONFLICT (root_thread_id) DO UPDATE SET metadata = EXCLUDED.metadata",
                &[&root_thread_id.as_str(), &metadata],
            )
            .await?;
        Ok(())
    }

    async fn get_active_subscriptions(
        &self,
        thread_id: &ThreadId<str>,
    ) -> Result<Vec<ToolCallId>, StoreError> {
        let rows = self
            .client()
            .query(
                "SELECT tool_call_id FROM infinity_subscriptions WHERE thread_id = $1",
                &[&thread_id.as_str()],
            )
            .await?;
        Ok(rows
            .iter()
            .map(|row| ToolCallId::from(row.get::<_, String>(0)))
            .collect())
    }

    async fn add_active_subscription(
        &self,
        thread_id: &ThreadId<str>,
        tool_call_id: &ToolCallId<str>,
    ) -> Result<(), StoreError> {
        self.client().execute(
                "INSERT INTO infinity_subscriptions (thread_id, tool_call_id) VALUES ($1, $2) ON CONFLICT DO NOTHING",
                &[&thread_id.as_str(), &tool_call_id.as_str()],
            )
            .await?;
        Ok(())
    }

    async fn remove_active_subscription(
        &self,
        thread_id: &ThreadId<str>,
        tool_call_id: &ToolCallId<str>,
    ) -> Result<(), StoreError> {
        self.client()
            .execute(
                "DELETE FROM infinity_subscriptions WHERE thread_id = $1 AND tool_call_id = $2",
                &[&thread_id.as_str(), &tool_call_id.as_str()],
            )
            .await?;
        Ok(())
    }

    async fn add_pending_user_choice(
        &self,
        thread_id: &ThreadId<str>,
        choice: UserChoice,
    ) -> Result<(), StoreError> {
        // A choice with the same ID is replaced in place, keeping its position.
        let body = serde_json::to_value(&choice)?;
        self.client().execute(
                "INSERT INTO infinity_user_choices (thread_id, choice_id, choice) VALUES ($1, $2, $3)
                 ON CONFLICT (thread_id, choice_id) DO UPDATE SET choice = EXCLUDED.choice",
                &[&thread_id.as_str(), &choice.id.as_str(), &body],
            )
            .await?;
        Ok(())
    }

    async fn remove_pending_user_choice(
        &self,
        thread_id: &ThreadId<str>,
        choice_id: &ChoiceId<str>,
    ) -> Result<(), StoreError> {
        self.client()
            .execute(
                "DELETE FROM infinity_user_choices WHERE thread_id = $1 AND choice_id = $2",
                &[&thread_id.as_str(), &choice_id.as_str()],
            )
            .await?;
        Ok(())
    }

    async fn get_pending_user_choices(
        &self,
        thread_id: &ThreadId<str>,
    ) -> Result<Vec<UserChoice>, StoreError> {
        let rows = self
            .client()
            .query(
                "SELECT choice FROM infinity_user_choices WHERE thread_id = $1 ORDER BY seq",
                &[&thread_id.as_str()],
            )
            .await?;
        rows.iter()
            .map(|row| Ok(serde_json::from_value(row.get::<_, serde_json::Value>(0))?))
            .collect()
    }
}
