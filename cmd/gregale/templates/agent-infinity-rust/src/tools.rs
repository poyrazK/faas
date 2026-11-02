// The starter's one asynchronous tool, `lookup`, dispatched over RAP. Its
// callback URL is issued for the calling thread just before the call goes
// out, so the result can only land in that thread.
//
// With TOOL_URL set, the invocation is POSTed to that RAP tool server. Without
// it, the call is held in Postgres: GET /threads/:id shows it with its
// callback URL, and your backend posts the RAP tool_result there.
//
// Gregale's own tools, such as gregale_sleep, come from its RAP toolset
// (PlatformTool). Gregale answers them through the same callback URLs.

use std::sync::Arc;

use async_trait::async_trait;
use infinity_agent_core::tools::rap_tool::{
    RapInvocationParams, RapToolDescriptor, invoke_rap_tool,
};
use infinity_agent_core::tools::{Tool, ToolContext};
use rap_client::http::SimpleHttpClient;
use rap_protocol::{ProviderCallId, ToolCallId, ToolsetManifest};

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

/// One tool of Gregale's RAP toolset. The toolset's invoke endpoint checks
/// the callback URL the call carries, so the call needs no key either.
pub struct PlatformTool {
    pub descriptor: RapToolDescriptor,
    pub endpoint: String,
    pub platform: Arc<Platform>,
    pub endpoint_id: String,
    pub http: SimpleHttpClient,
}

/// The toolset's tools, ready for the agent system.
pub fn platform_tools(
    toolset: ToolsetManifest,
    platform: &Arc<Platform>,
    endpoint_id: &str,
) -> Vec<Box<dyn Tool<GregaleSender>>> {
    let endpoint = toolset.endpoint;
    toolset
        .tools
        .into_iter()
        .map(|def| {
            Box::new(PlatformTool {
                descriptor: def.into(),
                endpoint: endpoint.clone(),
                platform: platform.clone(),
                endpoint_id: endpoint_id.to_owned(),
                http: SimpleHttpClient::new(),
            }) as Box<dyn Tool<GregaleSender>>
        })
        .collect()
}

#[async_trait]
impl Tool<GregaleSender> for PlatformTool {
    fn name(&self) -> &str {
        &self.descriptor.name
    }

    fn description(&self) -> &str {
        &self.descriptor.description
    }

    fn parameters(&self) -> serde_json::Value {
        self.descriptor.parameters.clone()
    }

    fn display_script(&self) -> Option<&str> {
        self.descriptor.display_script.as_deref()
    }

    async fn execute(
        &self,
        args: serde_json::Value,
        id: ToolCallId,
        call_id: Option<ProviderCallId>,
        context: &ToolContext<GregaleSender>,
    ) -> Result<(), BoxError> {
        let callback_url = self
            .platform
            .issue_callback(&self.endpoint_id, context.group_id.as_str())
            .await?;
        let params = RapInvocationParams {
            endpoint: &self.endpoint,
            operation: &self.descriptor.name,
            arguments: args,
            id,
            call_id,
            callback_url: Some(&callback_url),
        };
        invoke_rap_tool(&self.http, params, context).await
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_toolset_becomes_tools() {
        let manifest: ToolsetManifest = serde_json::from_value(serde_json::json!({
            "name": "gregale",
            "endpoint": "https://api.gregale.dev/v1/agent-tools/invoke",
            "tools": [{
                "name": "gregale_sleep",
                "description": "Wake this thread later.",
                "inputSchema": { "type": "object", "properties": { "seconds": { "type": "number" } } },
                "displayScript": "\"Sleeping\"",
            }],
        }))
        .unwrap();
        let tools = platform_tools(manifest, &Platform::from_env(), "ep-1");
        assert_eq!(tools.len(), 1);
        assert_eq!(tools[0].name(), "gregale_sleep");
        assert_eq!(tools[0].parameters()["type"], "object");
        assert_eq!(tools[0].display_script(), Some("\"Sleeping\""));
    }
}
