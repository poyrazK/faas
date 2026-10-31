# ADR-20261019200059: HTTP service edges as binding evidence

- **Status:** accepted
- **Date:** 2026-10-19
- **Related:** ADR-276, ADR-1645, ADR-1646, ADR-20261019152416
- **Problem:** ADR-276 keeps plain HTTP as the default service-binding transport. HTTPS is a per-caller opt-in that needs the private HTTPS listener and the workload CA on the node. No deployment manifest enables that listener, including the production template. Binding evidence was HTTPS-only nonetheless:
  - `gregale bindings probe` and the managed probe of ADR-1645 required a TLS handshake.
  - Candidate qualification (ADR-1646) recorded service receipts only for HTTPS callers, and blocked any graph with a plain HTTP service edge.

  So an app on default settings could never verify a service binding, and an enforced graph with service edges could never activate.
- **Decision:** A service edge's evidence uses the transport its caller is configured for. HTTPS callers keep the existing TLS contract. HTTP callers are verified over the plaintext service proxy at `http://<service>.svc.gregale:10081`, the URL their `GREGALE_SERVICE_<NAME>_URL` holds. Making HTTPS the default was rejected: no deployment runs the private HTTPS listener, so new apps would lose their service calls.
- **Why HTTP evidence is equivalent:**
  - The gateway identifies the caller by its node and host-bridge address on both listeners, and resolves the same declared binding, target policy and route.
  - TLS on this hop protects only the point-to-point link inside the caller's own network namespace. It never authenticated the caller.
  - The gateway still refuses plaintext from a caller configured for HTTPS, so evidence cannot be downgraded.
- **Probes:**
  - The guest probe follows the caller's effective transport. In HTTP mode it requires the canonical `_URL` to equal the plaintext proxy URL, resolves `<service>.svc.gregale`, and sends the same marked `HEAD` probe without TLS.
  - The report gains `transport`. Its TLS check is `not_checked`, and the stored verification omits the TLS check.
  - The gateway now recognizes a probe on the `svc.gregale` host form as well as the `.internal` alias. A probe on either requires the caller's declared binding, as the alias path always has, so account-wide discovery cannot satisfy it.
- **Managed probes:**
  - The managed probe spec gains `transport`. It is empty for HTTPS, so existing pins and receipts are unchanged.
  - An HTTP edge advertises the `managed-http-v1` contract.
  - Guests that predate this decision reject the unknown field and report an invalid probe. Version skew therefore fails closed.
- **Qualification:**
  - The private qualification proxy records a service receipt for any completed 2xx exchange. Previously it did so for HTTPS routes only.
  - The receipt guard and activation authority require the receipt's `RequireHTTPS` to equal the caller's effective frozen transport, replacing the requirement that it be true.
  - The rule that blocked graphs whose caller transport was not HTTPS is removed.
- **Limits:**
  - HTTP evidence does not show that a workload trusts the service CA. That remains part of the HTTPS contract only.
  - Native KVM acceptance and qualification dispatch remain pending.
- **Validation:**
  - Portable gateway tests cover HTTP probes on the `svc.gregale` host, the declared-binding requirement, and HTTP qualification receipts.
  - API tests cover report and spec validation.
  - Memory and PostgreSQL tests activate an enforced graph whose edges use HTTP, and reject a receipt whose transport does not match the caller.
  - Guest probe tests are Linux-only and run in CI.
