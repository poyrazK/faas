# Retry project rollback starts by request ID

Status: Accepted

The CLI prints a request UUID before it submits a project rollback. `gregale project rollback <slug> --yes --request-id <uuid>` reuses it across invocations. The request ID, start options, and rollback receipt commit in the same insert. Request IDs are unique within the project. A retry with the accepted options returns the current operation receipt, even after completion. Different options conflict. A preflight refusal leaves the request ID available for retry.

The API accepts the request UUID as `request_id` on start. The existing idempotency response cache still handles older requests without this field. Keyed starts read their durable receipt, including when a response or controller was lost after admission.
