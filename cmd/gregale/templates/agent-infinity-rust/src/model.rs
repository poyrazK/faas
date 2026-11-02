// A scripted model, so the starter runs with no provider key: a user message
// calls `lookup` with the message as its query, and the tool's result is the
// answer. "sleep N" calls gregale_sleep for N seconds instead, when Gregale's
// toolset is loaded. Replace it with any Infinity provider, for example
// infinity-provider-rig pointed at your Gregale AI integration.

use std::sync::Arc;

use async_trait::async_trait;
use infinity_agent_core::system::StaticModel;
use infinity_provider_protocol::{
    AssistantContent, CompletionError, CompletionModel, CompletionRequest, FinalResponse, Message,
    ModelEntry, ModelStream, SingleModelProvider, StreamChunk, ToolCall, ToolResultContent,
    UserContent,
};

pub struct ScriptedModel;

pub const MODEL_ID: &str = "scripted";
/// Gregale's sleep tool (ADR-20261030125501 §4).
pub const SLEEP_TOOL: &str = "gregale_sleep";

pub fn scripted() -> StaticModel {
    let entry = ModelEntry {
        model_id: MODEL_ID.to_owned(),
        display_name: "Scripted lookup".to_owned(),
        context_window: 200_000,
        max_output_tokens: None,
        supports_image_input: false,
    };
    StaticModel::from_entry(
        Arc::new(SingleModelProvider::new(entry.clone(), ScriptedModel)),
        &entry,
    )
}

/// The next step for a history: answer the last tool result, or act on the
/// last user text.
pub fn next_step(request: &CompletionRequest) -> StreamChunk {
    let last = request.chat_history.iter().rev().find_map(|m| match m {
        Message::User { content } => content.last().cloned(),
        Message::Assistant { .. } => None,
    });
    match last {
        Some(UserContent::ToolResult(result)) => {
            let text: Vec<String> = result
                .content
                .iter()
                .filter_map(|c| match c {
                    ToolResultContent::Text(t) => Some(t.text.clone()),
                    ToolResultContent::Image(_) => None,
                })
                .collect();
            let tool = tool_name(request, &result.id).unwrap_or("tool");
            StreamChunk::Text(format!("{tool} answered: {}", text.join("\n")))
        }
        Some(UserContent::Text(t)) => {
            let id = format!("call_{}", &uuid::Uuid::new_v4().simple().to_string()[..24]);
            let offered = request.tools.iter().any(|tool| tool.name == SLEEP_TOOL);
            match sleep_seconds(&t.text).filter(|_| offered) {
                Some(seconds) => StreamChunk::ToolCall(ToolCall::new(
                    id,
                    SLEEP_TOOL,
                    serde_json::json!({ "seconds": seconds, "reason": "asked to sleep" }),
                )),
                None => StreamChunk::ToolCall(ToolCall::new(
                    id,
                    "lookup",
                    serde_json::json!({ "query": t.text }),
                )),
            }
        }
        _ => StreamChunk::Text("Send a question to start.".to_owned()),
    }
}

/// The tool a result answers: the name on the history's call of that ID.
fn tool_name<'a>(request: &'a CompletionRequest, id: &str) -> Option<&'a str> {
    request.chat_history.iter().rev().find_map(|m| match m {
        Message::Assistant { content } => content.iter().find_map(|c| match c {
            AssistantContent::ToolCall(call) if call.id == id => Some(call.function.name.as_str()),
            _ => None,
        }),
        Message::User { .. } => None,
    })
}

/// "sleep 30" asks for 30 seconds.
fn sleep_seconds(text: &str) -> Option<u64> {
    text.trim()
        .strip_prefix("sleep ")?
        .trim()
        .parse()
        .ok()
        .filter(|s| *s >= 1)
}

#[async_trait]
impl CompletionModel for ScriptedModel {
    async fn stream(&self, request: CompletionRequest) -> Result<ModelStream, CompletionError> {
        let chunks = vec![
            Ok(next_step(&request)),
            Ok(StreamChunk::Final(FinalResponse { usage: None })),
        ];
        Ok(Box::pin(futures_util::stream::iter(chunks)))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use infinity_provider_protocol::{Text, ToolDefinition, ToolResult};

    fn request(content: UserContent) -> CompletionRequest {
        CompletionRequest {
            chat_history: vec![Message::User {
                content: vec![content],
            }],
            ..Default::default()
        }
    }

    /// The history after a call and its result.
    fn answered(call: ToolCall, text: &str) -> CompletionRequest {
        let result = UserContent::ToolResult(ToolResult {
            id: call.id.clone(),
            call_id: None,
            content: vec![ToolResultContent::Text(Text {
                text: text.to_owned(),
            })],
        });
        CompletionRequest {
            chat_history: vec![
                Message::Assistant {
                    content: vec![AssistantContent::ToolCall(call)],
                },
                Message::User {
                    content: vec![result],
                },
            ],
            ..Default::default()
        }
    }

    #[test]
    fn a_question_calls_lookup_and_a_result_answers() {
        let StreamChunk::ToolCall(call) =
            next_step(&request(UserContent::text("capital of Malta?")))
        else {
            panic!("want a tool call")
        };
        assert_eq!(call.function.name, "lookup");
        assert_eq!(call.function.arguments["query"], "capital of Malta?");
        assert_eq!(
            next_step(&answered(call, "Valletta")),
            StreamChunk::Text("lookup answered: Valletta".to_owned())
        );
    }

    #[test]
    fn sleep_calls_gregale_sleep_when_it_is_offered() {
        let StreamChunk::ToolCall(call) = next_step(&request(UserContent::text("sleep 30"))) else {
            panic!("want a tool call")
        };
        assert_eq!(call.function.name, "lookup", "no toolset, no sleep");

        let mut offered = request(UserContent::text("sleep 30"));
        offered.tools = vec![ToolDefinition {
            name: SLEEP_TOOL.to_owned(),
            description: "Wake this thread later.".to_owned(),
            parameters: serde_json::json!({ "type": "object" }),
        }];
        let StreamChunk::ToolCall(call) = next_step(&offered) else {
            panic!("want a tool call")
        };
        assert_eq!(call.function.name, SLEEP_TOOL);
        assert_eq!(call.function.arguments["seconds"], 30);
        assert_eq!(
            next_step(&answered(call, "Slept for 30 seconds.")),
            StreamChunk::Text("gregale_sleep answered: Slept for 30 seconds.".to_owned())
        );
    }
}
