# Environment operator notes

APID hosted Task binding qualification is separately opt-in with `FAAS_MCP_HOSTED_TASK_BINDING_VERIFICATION=1` and requires `FAAS_MCP_HOSTED_TASK_SOURCE_RETENTION=1`. It performs owner-selected read-only native managed PostgreSQL catalog checks; agents receive metadata only and no execution authority. See [hosted Task bindings](../mcp.md#owner-qualified-hosted-task-bindings).
