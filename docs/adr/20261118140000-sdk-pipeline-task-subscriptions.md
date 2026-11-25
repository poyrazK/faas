# ADR-20261118140000 SDK pipeline Task subscriptions

Applications mounted in the Node and Python MCP starters can accept pipeline
Task IDs in `subscriptions/listen`. The acknowledgement and notifications use
the authenticated Task snapshot from the SDK. Form capabilities come from the
subscription request. The stream includes the same typed clarification and
result as a direct read by that caller.

Native Task changes continue using their existing notification source. Pipeline
Task subscriptions on either host refresh current receipts, workflow status,
and gate permissions using bounded polling. The snapshot comparison ignores
the read timestamp. Current access, Task expiry, and the subscription's token
lifetime govern delivery. Reconnecting to any replica begins with a fresh
snapshot from the shared stores.
