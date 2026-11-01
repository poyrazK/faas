// Store contract and step tests against a real Postgres. They run when
// INFINITY_TEST_DATABASE_URL names a database the tests may write to, and
// skip otherwise:
//
//   INFINITY_TEST_DATABASE_URL=postgres://localhost/agent_test?sslmode=disable cargo test

use async_trait::async_trait;
use infinity_agent_core::ThreadId;
use infinity_agent_core::message::{InfinityMessage, InputMessage};
use infinity_agent_core::system::{AgentSystemBuilder, EventCollector, NoDeferral, UserChoice};
use infinity_agent_core::tools::{Tool, ToolContext};
use infinity_agent_core::traits::{ConversationStore, SpawnContext, StateStore};
use infinity_provider_protocol::{Message, UserContent};
use rap_protocol::{ProviderCallId, RapCallback, RapToolResult, ToolCallId};

use crate::platform::{GregaleSender, Platform};
use crate::store::{Db, PgConversationStore, PgStateStore};

async fn db() -> Option<Db> {
    let url = std::env::var("INFINITY_TEST_DATABASE_URL").ok()?;
    // SAFETY: the tests that set it run before any thread reads it.
    unsafe { std::env::set_var("DATABASE_URL", url) };
    Some(
        crate::connect()
            .await
            .expect("connect to INFINITY_TEST_DATABASE_URL"),
    )
}

fn fresh(name: &str) -> ThreadId {
    ThreadId::from(format!("{name}-{}", uuid::Uuid::new_v4().simple()))
}

#[tokio::test]
async fn conversation_store_contract() {
    let Some(db) = db().await else { return };
    let store = PgConversationStore(db);
    let root = fresh("root");
    store.ensure_root_thread(&root).await.unwrap();
    store.ensure_root_thread(&root).await.unwrap();
    assert!(store.thread_exists(&root).await.unwrap());
    assert!(!store.thread_exists(&fresh("none")).await.unwrap());

    let texts = ["a", "b", "c", "d"];
    let batch = |xs: &[&str]| {
        xs.iter()
            .map(|t| {
                (
                    InfinityMessage::User {
                        content: UserContent::text(*t),
                    },
                    format!("id-{t}"),
                )
            })
            .collect::<Vec<_>>()
    };
    store
        .append_messages(&root, batch(&texts[..2]))
        .await
        .unwrap();
    store
        .append_messages(&root, batch(&texts[2..]))
        .await
        .unwrap();
    let text_of = |m: &InfinityMessage| match m {
        InfinityMessage::User {
            content: UserContent::Text(t),
        } => t.text.clone(),
        other => panic!("unexpected {other:?}"),
    };
    let load = |from, to| {
        let store = store.clone();
        let root = root.clone();
        async move {
            store
                .load_history_up_to(&root, from, to)
                .await
                .unwrap()
                .iter()
                .map(text_of)
                .collect::<Vec<_>>()
        }
    };
    assert_eq!(load(None, None).await, ["a", "b", "c", "d"]);
    assert_eq!(
        load(Some(1), Some(3)).await,
        ["b", "c"],
        "skip one, keep three leading"
    );
    assert_eq!(load(None, Some(2)).await, ["a", "b"]);

    // A child inherits two messages; a grandchild one of the child's.
    let child = store
        .spawn_thread(
            &root,
            ToolCallId::from_ref("spawn-1"),
            false,
            SpawnContext::InheritUpTo(2),
        )
        .await
        .unwrap();
    let grandchild = store
        .spawn_thread(
            &child,
            ToolCallId::from_ref("spawn-2"),
            true,
            SpawnContext::Fresh,
        )
        .await
        .unwrap();
    assert_eq!(
        store.get_ancestor_chain(&grandchild).await.unwrap(),
        vec![(root.clone(), 2), (child.clone(), 0)],
        "root first, with how much of each parent its child inherits"
    );
    assert_eq!(
        store.get_thread_parent_info(&child).await.unwrap(),
        Some((root.clone(), ToolCallId::from("spawn-1".to_owned())))
    );
    assert_eq!(store.get_thread_parent_info(&root).await.unwrap(), None);
    assert!(
        store
            .is_subscription_event_thread(&grandchild)
            .await
            .unwrap()
    );
    assert!(store.is_fresh_context_thread(&grandchild).await.unwrap());
    assert_eq!(store.get_thread_spawn_order(&child).await.unwrap(), Some(2));
    let (history, _, prefix) = store.load_history_with_ancestors(&child).await.unwrap();
    assert_eq!(
        (history.len(), prefix),
        (2, 2),
        "the child sees its parent's first two messages"
    );

    assert!(!store.is_thread_closed(&child).await.unwrap());
    store.close_thread(&child).await.unwrap();
    assert!(store.is_thread_closed(&child).await.unwrap());
    store.mark_thread_as_compaction(&child).await.unwrap();
    assert!(store.is_compaction_thread(&child).await.unwrap());

    store
        .save_compaction_summary(&root, "first", 2)
        .await
        .unwrap();
    store
        .save_compaction_summary(&root, "second", 4)
        .await
        .unwrap();
    assert_eq!(
        store
            .load_latest_compaction_summary_up_to(&root, None)
            .await
            .unwrap(),
        Some(("second".to_owned(), 4))
    );
    assert_eq!(
        store
            .load_latest_compaction_summary_up_to(&root, Some(3))
            .await
            .unwrap(),
        Some(("first".to_owned(), 2))
    );
    assert_eq!(
        store
            .load_latest_compaction_summary_up_to(&root, Some(1))
            .await
            .unwrap(),
        None
    );
}

#[tokio::test]
async fn state_store_contract() {
    let Some(db) = db().await else { return };
    let store = PgStateStore(db);
    let thread = fresh("state");
    store
        .add_processed_message_ids(&thread, vec!["m1".into(), "m2".into()])
        .await
        .unwrap();
    store
        .add_processed_message_ids(&thread, vec!["m2".into(), "m3".into()])
        .await
        .unwrap();
    let mut ids: Vec<_> = store
        .get_processed_ids(&thread)
        .await
        .unwrap()
        .into_iter()
        .collect();
    ids.sort();
    assert_eq!(ids, ["m1", "m2", "m3"]);

    assert_eq!(store.get_metadata(&thread).await.unwrap(), None);
    store
        .set_metadata(&thread, serde_json::json!({ "n": 1 }))
        .await
        .unwrap();
    store
        .set_metadata(&thread, serde_json::json!({ "n": 2 }))
        .await
        .unwrap();
    assert_eq!(
        store.get_metadata(&thread).await.unwrap(),
        Some(serde_json::json!({ "n": 2 }))
    );

    let call = ToolCallId::from_ref("sub-1");
    store.add_active_subscription(&thread, call).await.unwrap();
    store.add_active_subscription(&thread, call).await.unwrap();
    assert_eq!(
        store.get_active_subscriptions(&thread).await.unwrap().len(),
        1
    );
    store
        .remove_active_subscription(&thread, call)
        .await
        .unwrap();
    assert!(
        store
            .get_active_subscriptions(&thread)
            .await
            .unwrap()
            .is_empty()
    );

    let choice = |id: &str, prompt: &str| UserChoice {
        id: id.to_owned().into(),
        prompt: prompt.to_owned(),
        choices: vec!["yes".into(), "no".into()],
        default: 0,
        response_url: "https://example.test".into(),
    };
    store
        .add_pending_user_choice(&thread, choice("c1", "first"))
        .await
        .unwrap();
    store
        .add_pending_user_choice(&thread, choice("c2", "second"))
        .await
        .unwrap();
    store
        .add_pending_user_choice(&thread, choice("c1", "replaced"))
        .await
        .unwrap();
    let prompts: Vec<_> = store
        .get_pending_user_choices(&thread)
        .await
        .unwrap()
        .into_iter()
        .map(|c| c.prompt)
        .collect();
    assert_eq!(
        prompts,
        ["replaced", "second"],
        "a replaced choice keeps its place"
    );
    store
        .remove_pending_user_choice(&thread, ToolCallId::from_ref("c1"))
        .await
        .unwrap();
    assert_eq!(
        store.get_pending_user_choices(&thread).await.unwrap().len(),
        1
    );
}

/// Holds its call in the store, as `lookup` does without TOOL_URL, but needs
/// no Gregale API to issue a callback URL.
struct HeldTool(Db);

#[async_trait]
impl Tool<GregaleSender> for HeldTool {
    fn name(&self) -> &str {
        "lookup"
    }
    fn description(&self) -> &str {
        "Look up a fact."
    }
    fn parameters(&self) -> serde_json::Value {
        serde_json::json!({ "type": "object", "properties": { "query": { "type": "string" } } })
    }
    async fn execute(
        &self,
        args: serde_json::Value,
        id: ToolCallId,
        _call_id: Option<ProviderCallId>,
        context: &ToolContext<GregaleSender>,
    ) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        let invocation = serde_json::json!({ "operation": "lookup", "arguments": args, "id": id });
        Ok(self
            .0
            .hold_tool_call(context.group_id.as_str(), id.as_str(), &invocation)
            .await?)
    }
}

/// Two slices of one thread through `AgentSystem::step` on these stores: a
/// question dispatches `lookup`, and its RAP result produces the answer. A
/// redelivered message changes nothing.
#[tokio::test]
async fn two_slices_answer_through_step_mode() {
    let Some(db) = db().await else { return };
    let thread = fresh("agent");
    let step = |input: InputMessage, dedup: &str| {
        let db = db.clone();
        let dedup = dedup.to_owned();
        async move {
            let sender = GregaleSender {
                platform: Platform::from_env(),
                endpoint_id: "ep-test".into(),
            };
            let mut system = AgentSystemBuilder::new(
                PgConversationStore(db.clone()),
                PgStateStore(db.clone()),
                crate::model::scripted(),
                sender,
            )
            .tools([Box::new(HeldTool(db)) as Box<dyn Tool<GregaleSender>>])
            .build();
            system
                .step(
                    vec![(input, dedup)],
                    &EventCollector::new(),
                    &mut NoDeferral,
                )
                .await
                .unwrap();
        }
    };
    let question = InputMessage::user_text(thread.clone(), "capital of Malta?");
    step(question.clone(), "q-1").await;
    step(question, "q-1").await;
    let view = db.thread_view(thread.as_str()).await.unwrap();
    let held = view["held_tool_calls"].as_array().unwrap();
    assert_eq!(
        (view["messages"].as_array().unwrap().len(), held.len()),
        (2, 1),
        "{view}"
    );
    let call_id = held[0]["id"].as_str().unwrap().to_owned();

    let result = RapCallback::ToolResult(RapToolResult {
        group_id: thread.clone(),
        id: ToolCallId::from(call_id.clone()),
        call_id: None,
        text: Some("Valletta".into()),
        content: None,
        display_as: None,
        subscription: None,
    });
    let infinity_rap_bridge::CallbackDelivery::Input { message, .. } =
        infinity_rap_bridge::prepare_callback(result)
    else {
        panic!("a tool result is agent input")
    };
    step(*message, &format!("rap-tool-result-{call_id}")).await;
    let history = PgConversationStore(db.clone())
        .load_history_up_to(&thread, None, None)
        .await
        .unwrap();
    let last = history
        .last()
        .map(|m| m.clone().into_messages())
        .unwrap_or_default();
    assert!(
        matches!(last.as_slice(), [Message::Assistant { content }] if format!("{content:?}").contains("lookup answered: Valletta")),
        "{history:?}"
    );
    let view = db.thread_view(thread.as_str()).await.unwrap();
    assert!(
        view["held_tool_calls"].as_array().unwrap().is_empty(),
        "an answered call is no longer held: {view}"
    );
}
