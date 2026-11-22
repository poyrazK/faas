// Inbox records to Infinity input (ADR-20261030125501 §2). The realtime
// trigger posts one record per invocation: the endpoint, the message's
// metadata (thread_id, message_id, kind) and its data.
//
// - kind user: your backend's message, data {"text": "..."}.
// - kind tool_result or event from a RAP tool: data is the RAP callback
//   itself, converted with Infinity's own RAP bridge.
// - kind event with data {"infinity_input": ...}: the runtime's loopback
//   (platform.rs), an InputMessage it sent itself.

use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use infinity_agent_core::message::InputMessage;
use infinity_rap_bridge::{CallbackDelivery, prepare_callback};
use rap_protocol::RapCallback;
use serde::Deserialize;

#[derive(Deserialize)]
struct RawRecord {
    endpoint_id: String,
    #[serde(default)]
    metadata: std::collections::HashMap<String, String>,
    #[serde(default)]
    data_base64: String,
}

pub struct Record {
    pub endpoint_id: String,
    pub thread_id: String,
    /// The inbox message_id. It is stable across redeliveries, so it is the
    /// input's dedup ID.
    pub message_id: String,
    pub input: Option<InputMessage>,
    /// Set when the data is the stub of a stored large tool result
    /// (ADR-20261030125501 §3 amendment 2026-11-22): the slice reads the
    /// result and builds its input from it (stored_input).
    pub stored: Option<StoredResult>,
}

/// A stored large tool result's URL, size and digest, from its stub.
#[derive(Debug, Clone, PartialEq)]
pub struct StoredResult {
    pub url: String,
    pub bytes: usize,
    pub sha256: String,
}

fn stored_result(kind: &str, data: &serde_json::Value) -> Option<StoredResult> {
    if !matches!(kind, "tool_result" | "event") || data.get("truncated")?.as_bool() != Some(true) {
        return None;
    }
    Some(StoredResult {
        url: data.get("result_url")?.as_str()?.to_owned(),
        bytes: usize::try_from(data.get("result_bytes")?.as_u64()?).ok()?,
        sha256: data.get("result_sha256")?.as_str()?.to_owned(),
    })
}

/// The input of a stored result's full body, which must be a callback for
/// the same thread. None means it is not one.
pub fn stored_input(thread_id: &str, body: &[u8]) -> Option<Option<InputMessage>> {
    let data: serde_json::Value = serde_json::from_slice(body).ok()?;
    let input = runtime_input(data)?;
    if input
        .as_ref()
        .is_some_and(|m| m.group_id.as_str() != thread_id)
    {
        return None;
    }
    Some(input)
}

/// Parse a trigger record. None means the body is not an inbox record.
/// A record whose data is display-only (a RAP view update) has no input.
pub fn parse(body: &[u8]) -> Option<Record> {
    let raw: RawRecord = serde_json::from_slice(body).ok()?;
    let thread_id = raw.metadata.get("thread_id")?.clone();
    let message_id = raw.metadata.get("message_id")?.clone();
    let kind = raw.metadata.get("kind")?.as_str();
    let data: serde_json::Value =
        serde_json::from_slice(&STANDARD.decode(raw.data_base64).ok()?).ok()?;
    let stored = stored_result(kind, &data);
    let input = match kind {
        "user" => Some(InputMessage::user_text(
            thread_id.clone(),
            user_text(&data)?,
        )),
        "tool_result" | "event" => runtime_input(data)?,
        _ => return None,
    };
    // A message names its own thread; it must be the lane it arrived in.
    if input
        .as_ref()
        .is_some_and(|m| m.group_id.as_str() != thread_id)
    {
        return None;
    }
    Some(Record {
        endpoint_id: raw.endpoint_id,
        thread_id,
        message_id,
        input,
        stored,
    })
}

fn user_text(data: &serde_json::Value) -> Option<String> {
    match data {
        serde_json::Value::String(s) => Some(s.clone()),
        other => other.get("text")?.as_str().map(str::to_owned),
    }
}

/// Some(None) is a valid callback with no agent input.
fn runtime_input(mut data: serde_json::Value) -> Option<Option<InputMessage>> {
    if let Some(input) = data.get_mut("infinity_input") {
        return serde_json::from_value(input.take()).ok().map(Some);
    }
    let callback: RapCallback = serde_json::from_value(data).ok()?;
    match prepare_callback(callback) {
        CallbackDelivery::Input { message, .. } => Some(Some(*message)),
        CallbackDelivery::ViewUpdate(_) => Some(None),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use infinity_agent_core::message::InputMessageContent;
    use infinity_provider_protocol::UserContent;

    fn record(kind: &str, thread: &str, data: serde_json::Value) -> Vec<u8> {
        serde_json::to_vec(&serde_json::json!({
            "endpoint_id": "ep-1",
            "channel": "agent-inbox",
            "metadata": { "thread_id": thread, "message_id": "m-1", "kind": kind },
            "data_base64": STANDARD.encode(serde_json::to_vec(&data).unwrap()),
        }))
        .unwrap()
    }

    #[test]
    fn user_text_and_rap_results_become_input() {
        let user = parse(&record("user", "t-1", serde_json::json!({ "text": "hi" }))).unwrap();
        assert_eq!(
            (
                user.endpoint_id.as_str(),
                user.thread_id.as_str(),
                user.message_id.as_str()
            ),
            ("ep-1", "t-1", "m-1")
        );
        assert!(
            matches!(user.input.unwrap().content, InputMessageContent::User(UserContent::Text(t)) if t.text == "hi")
        );

        let rap = serde_json::json!({ "type": "tool_result", "group_id": "t-1", "id": "call_1", "text": "Valletta" });
        let result = parse(&record("tool_result", "t-1", rap))
            .unwrap()
            .input
            .unwrap();
        assert!(
            matches!(result.content, InputMessageContent::User(UserContent::ToolResult(r)) if r.id == "call_1")
        );

        let looped =
            serde_json::json!({ "infinity_input": InputMessage::user_text("t-1", "again") });
        assert!(
            parse(&record("event", "t-1", looped))
                .unwrap()
                .input
                .is_some()
        );
    }

    #[test]
    fn a_stored_result_stub_is_noted() {
        let stub = serde_json::json!({ "type": "tool_result", "group_id": "t-1", "id": "call_1", "text": "Vall",
            "truncated": true, "result_url": "https://api.example.test/v1/agent-callbacks/results/x",
            "result_bytes": 9000, "result_sha256": "ab" });
        let rec = parse(&record("tool_result", "t-1", stub)).unwrap();
        assert_eq!(
            rec.stored,
            Some(StoredResult {
                url: "https://api.example.test/v1/agent-callbacks/results/x".to_owned(),
                bytes: 9000,
                sha256: "ab".to_owned()
            })
        );
        let plain = serde_json::json!({ "type": "tool_result", "group_id": "t-1", "id": "call_1", "text": "x" });
        assert!(
            parse(&record("tool_result", "t-1", plain))
                .unwrap()
                .stored
                .is_none()
        );

        let full = serde_json::to_vec(&serde_json::json!({ "type": "tool_result", "group_id": "t-1", "id": "call_1", "text": "Valletta" })).unwrap();
        assert!(matches!(stored_input("t-1", &full), Some(Some(_))));
        assert!(
            stored_input("t-2", &full).is_none(),
            "a result for another thread"
        );
        assert!(stored_input("t-1", b"not json").is_none());
    }

    #[test]
    fn other_lanes_and_shapes_are_refused() {
        let rap =
            serde_json::json!({ "type": "tool_result", "group_id": "t-2", "id": "c", "text": "x" });
        assert!(
            parse(&record("tool_result", "t-1", rap)).is_none(),
            "a message for another thread"
        );
        assert!(
            parse(&record("shell", "t-1", serde_json::json!({}))).is_none(),
            "an unknown kind"
        );
        assert!(parse(b"{\"method\":\"GET\"}").is_none(), "not a record");
        let view = serde_json::json!({ "type": "view_update", "group_id": "t-1", "view_type": "p", "content": {} });
        assert!(
            parse(&record("event", "t-1", view))
                .unwrap()
                .input
                .is_none(),
            "display-only"
        );
    }
}
