# ADR 400: Store app-owned UDP listener intent

Status: proposed

Public UDP listeners need durable app/account ownership, a stable reserved port, and a namespace independent from TCP. Add an optional narrow listener-store interface, memory and PostgreSQL implementations, normalized DNS-safe names, and a replay-safe append-only migration. Default enabled state is false. The API and public UDP edge remain separate changes.

Creation locks the non-deleted app row, checks its account owner, and inserts in the same transaction. Database uniqueness reserves listener names per app and public UDP ports globally, including disabled listeners. Public lookup and enabled-listener enumeration exclude disabled intent, deleted apps, and account mismatches. Deletion releases the reserved identity/port. Integer range validation precedes conversion to generated SQL parameter types, preventing a large port from wrapping to an existing listener.

All production UDP listener queries use sqlc. The schema repeats protocol, name, and port constraints. Memory and PostgreSQL tests cover normalization, ownership, collisions, independent TCP/UDP namespaces, deleted-app routing, and concurrent global port reservations. Direct SQL invalid updates are rejected without changing enabled intent. Migration tests cover default state and constraints; the repository replay gate checks ledger-loss replay. This establishes intent storage, not UDP delivery, native VM lifecycle, or public rollout acceptance.
