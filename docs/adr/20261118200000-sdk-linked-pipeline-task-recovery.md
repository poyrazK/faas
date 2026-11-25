# ADR-20261118200000 Linked recovery for failed pipeline Tasks

An application can publish recovery for a Task with an unavailable pinned
source. The recovery runtime resolves the original Task using its current
OAuth ownership and exact receipt. An application policy verifies
pre-effect failure evidence and returns the typed input and original source
digest. The existing preview service reviews the replacement's current data.

A server-derived replacement request ID binds the caller, original Task,
and recovery request ID. Confirmation uses the existing preview token
and durable native start receipt, then stores the original Task ID in
the replacement Task binding. A retry can repair the binding using the
accepted native receipt. The first slice reuses the original tool and
contract revision and requires explicit confirmation through MCP Tasks.
