# ADR 1072: Python realtime activity scopes

Status: Accepted; runtime validation deferred.

## Context

The server and Node SDK encode private activity scopes canonically, while Python applications otherwise recreate the encoding and can accidentally publish/subscribe to inconsistent channels.

## Decision

Export realtime_activity_scope_channel, parse_realtime_activity_scope and REALTIME_ACTIVITY_SCOPE_PREFIX from a handwritten, generator-preserved module. Encode UTF-8 parent and scope using unpadded base64url, separated under the __activity. prefix. Mirror Go validation: nonempty valid UTF-8, no leading/trailing Unicode White_Space, no NUL/slash/question/hash/CR/LF, parent at most 256 bytes, scope at most 64 bytes, final channel at most 256 bytes and no nested scoped parent.

Parse ordinary channels as None; strictly decode reserved channels, enforce UTF-8 and reconstruct the canonical string to reject padding, alternate alphabet, trailing bits and nesting. The account HTTP activity factory validates scoped inputs synchronously before making requests while retaining the original parent/scope route parameters.

## Consequences

Python publishers and subscribers share canonical scope identities. Helpers do not establish transport or grant access; read_activity and existing deploy-write/MFA authorization remain server-owned. HTTP sender identity remains backend. No routes, grants, schema or migration change. Static lint/compilation, SDK AST/export/preservation checks and server-validation inspection verify the implementation; runtime validation remains deferred by user request.
