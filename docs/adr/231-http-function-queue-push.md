# ADR-231 · Queue push delivery to HTTP functions

- **Status:** accepted
- **Date:** 2026-09-24
- **Decision:** Permit a queue binding with `workload_class=http` only when its
  mode is `push` and its app is a function. The existing trigger projection
  dispatches each record through gatewayd's HTTP function invocation path.
  Keep worker/job pull bindings unchanged. The bundled `queue-worker` starter
  uses the function shape and a single-record push batch.
- **Why:** The bundled starter is a Node handler, but declared a worker-class
  binding. A newly deployed function is HTTP-class, so the API rejected its
  binding before it could consume a message. Giving the function a worker
  label would not create the HTTP listener required by push dispatch.
- **Consequences:** The binding API and database admit HTTP push bindings but
  reject HTTP pull bindings and HTTP-class non-function apps. The starter no
  longer advertises queue-depth autoscaling or per-worker parallel delivery:
  the current trigger/gateway batch loop invokes records serially. One-message
  end-to-end delivery and retry/DLQ behavior must be checked on the next
  release before calling the starter production-verified. Parallel delivery
  and queue-driven scale-out require a separate bounded dispatch design and
  evidence gate.
- **Rejected alternatives:** Silencing the app/binding class mismatch would
  conceal a real transport incompatibility. Relabeling the Node function as
  a worker would leave it without the request listener push delivery needs.
  Claiming ten workers from `max_instances: 10` would be misleading while the
  push trigger delivers records serially.
