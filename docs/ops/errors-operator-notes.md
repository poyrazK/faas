# Customer error codes: operator notes

Server-side causes and remedies for codes on the customer
[errors page](../errors.md). They moved here so that page can be published
(#5709); customers are told to retry or contact support.

| Code | Operator remedy |
|---|---|
| `bindings_gate_unavailable` (503) | The binding catalogs cannot enforce the atomic promotion fence. Update the server and apply its migrations; managed PostgreSQL and traffic state must share the same database pool. |
| `outbound_probe_unavailable` (503) | The outbound probe policy catalog cannot be read or saved. Apply the server migrations and restore catalog availability. |
| `bindings_check_changed`, finding `queue_consumer_stale` | The scheduler stopped polling the queue consumer; restore scheduler polling. |
| `application_adoption_unknown` | A server that omits adoption facts reports this blocker for every binding; upgrade it. |
| 404 from `promote-with-application-ack` | The server predates strict promotion; upgrade it. Clients must not fall back to an ordinary traffic write. |
