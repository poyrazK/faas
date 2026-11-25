# ADR-20261118160000 SDK pipeline Task client

The Node and Python preview SDKs consume authenticated pipeline MCP Tasks
using the existing typed pipeline contract. A checkpoint contains the
Task and request IDs, tool name, and contract digest, and can be shared
across languages and hosts with the same authorization.

A client can use the Streamable HTTP adapter with an existing authenticated
HTTP client or supply its own MCP transport. It validates Task and receipt
bindings, clarification schemas, typed outputs, and named artifact links.
A watch reconnects once and falls back to authorized polling. One deadline
bounds observation, and starts and answers remain explicit calls.
