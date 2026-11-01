// The starter's one asynchronous tool, `lookup`, dispatched over RAP. Its
// callback URL is issued for the calling thread just before the call goes
// out, so the result can only land in that thread.
//
// With TOOL_URL set, the invocation is POSTed to that RAP tool server. Without
// it, the call is held in Postgres: GET /threads/:id shows it with its
// callback URL, and your backend posts the RAP tool_result there.

use std::sync::Arc;

use async_trait::async_trait;
use infinity_agent_core::tools::rap_tool::{RapInvocationParams, invoke_rap_tool};
use infinity_agent_core::tools::{Tool, ToolContext};
use rap_client::http::SimpleHttpClient;
use rap_protocol::{ProviderCallId, ToolCallId};

use crate::platform::{GregaleSender, Platform};
use crate::store::Db;

type BoxError = Box<dyn std::error::Error + Send + Sync>;

pub struct LookupTool {
    pub platform: Arc<Platform>,
    pub endpoint_id: String,
    pub db: Db,
    pub tool_url: Option<String>,
    pub http: SimpleHttpClient,
}

#[async_trait]
impl Tool<GregaleSender> for LookupTool {
    fn name(&self) -> &str {
        "lookup"
    }

    fn description(&self) -> &str {
        "Look up a fact. The answer arrives later, as the tool's result."
    }

    fn parameters(&self) -> serde_json::Value {
        serde_json::json!({
            "type": "object",
            "properties": { "query": { "type": "string", "description": "What to look up" } },
            "required": ["query"],
        })
    }

    async fn execute(
        &self,
        args: serde_json::Value,
        id: ToolCallId,
        call_id: Option<ProviderCallId>,
        context: &ToolContext<GregaleSender>,
    ) -> Result<(), BoxError> {
        let thread = context.group_id.as_str();
        let callback_url = self
            .platform
            .issue_callback(&self.endpoint_id, thread)
            .await?;
        if let Some(endpoint) = &self.tool_url {
            let params = RapInvocationParams {
                endpoint,
                operation: self.name(),
                arguments: args,
                id,
                call_id,
                callback_url: Some(&callback_url),
            };
            return invoke_rap_tool(&self.http, params, context).await;
        }
        // The same fields a RAP tool server would receive.
        let invocation = serde_json::json!({
            "operation": self.name(),
            "arguments": args,
            "id": id,
            "call_id": call_id,
            "callback_url": callback_url,
            "group_id": thread,
        });
        self.db
            .hold_tool_call(thread, id.as_str(), &invocation)
            .await?;
        Ok(())
    }
}
