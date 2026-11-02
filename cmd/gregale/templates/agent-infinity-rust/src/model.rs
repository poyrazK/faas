// A scripted model, so the starter runs with no provider key: a user message
// calls `lookup` with the message as its query, and the tool's result is the
// answer. Replace it with any Infinity provider, for example
// infinity-provider-rig pointed at your Gregale AI integration.

use std::sync::Arc;

use async_trait::async_trait;
use infinity_agent_core::system::StaticModel;
use infinity_provider_protocol::{
    CompletionError, CompletionModel, CompletionRequest, FinalResponse, Message, ModelEntry,
    ModelStream, SingleModelProvider, StreamChunk, ToolCall, ToolResultContent, UserContent,
};

pub struct ScriptedModel;

pub const MODEL_ID: &str = "scripted";

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

/// The next step for a history: answer the last tool result, or look up the
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
            StreamChunk::Text(format!("lookup answered: {}", text.join("\n")))
        }
        Some(UserContent::Text(t)) => {
            let id = format!("call_{}", &uuid::Uuid::new_v4().simple().to_string()[..24]);
            StreamChunk::ToolCall(ToolCall::new(
                id,
                "lookup",
                serde_json::json!({ "query": t.text }),
            ))
        }
        _ => StreamChunk::Text("Send a question to start.".to_owned()),
    }
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
    use infinity_provider_protocol::{Text, ToolResult};

    fn request(content: UserContent) -> CompletionRequest {
        CompletionRequest {
            chat_history: vec![Message::User {
                content: vec![content],
            }],
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
        let result = UserContent::ToolResult(ToolResult {
            id: call.id,
            call_id: None,
            content: vec![ToolResultContent::Text(Text {
                text: "Valletta".to_owned(),
            })],
        });
        assert_eq!(
            next_step(&request(result)),
            StreamChunk::Text("lookup answered: Valletta".to_owned())
        );
    }
}
