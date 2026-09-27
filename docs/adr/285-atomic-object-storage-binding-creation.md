# ADR-285 · Atomic object-storage binding creation

- **Status:** accepted
- **Date:** 2026-09-27
- **Decision:** Create a managed S3 compute binding's credential and all six sealed app secrets in one store operation, both for the direct API and project environment cloning. Seal all values before opening the transaction. PostgreSQL locks the ready bucket and owning app, checks credential and secret quotas, inserts the credential and six secrets, and commits once. A key conflict or other insert failure rolls back the entire transaction. The memory store checks every constraint before changing either map.
- **Why:** The previous endpoint committed a credential, then wrote six secrets in separate transactions. A process crash or late secret-key conflict could leave a usable S3 key with no complete runtime binding, and a retry could conflict with the orphaned binding.
- **Consequences:** Binding creation has a single visibility boundary. Existing standalone credential creation and binding rotation keep their separate interfaces. The handler's early quota and name checks still give precise errors; the store rechecks them for concurrent binding creates. Generic customer secret writes have their own quota path and do not share the new app lock.
- **Rejected alternatives:** Best-effort revocation and secret cleanup cannot recover from a process crash. Inserting a disabled credential first would require a second activation protocol and leave partial rows to reconcile.
