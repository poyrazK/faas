# ADR-20261019152416: Outbound integration bindings in candidate binding qualification

- **Status:** accepted
- **Date:** 2026-10-19
- **Related:** ADR-20261019121546, ADR-20261019072523, ADR-1646, ADR-997, ADR-1340
- **Problem:** ADR-1646 blocks enforced activation of a managed GitOps graph when a member has an outbound integration binding. Ordinary outbound evidence is a probe a running instance sends with its signed workload identity. A held candidate has no admissible identity, and its `QualificationOnly` network cannot reach the gateway. ADR-20261019121546 left two options: mint candidate identities, or open candidate egress to the gateway. Both change security boundaries.
- **Decision:** Neither option. outboundd sends the probe itself. A new in-process worker lists the probe-ready outbound bindings of enforced candidates whose qualification lease is live. It sends each binding's configured probe (`GET` or `HEAD`, its path, its expected 2xx status) through the gateway handler in process, under a qualification principal. Candidates keep the `QualificationOnly` network policy, and no identity is minted for a held deployment.
- **Qualification principal:** The principal is carried only in an in-process context value; no network request can carry it, as with the replay and internal principals of ADR-997 and ADR-1340. It names the account, app, request and attempt, and the probe's method and path. The gateway admits it only for:
  - an integration of the same account with managed provider auth
  - an integration attached to the app
  - exactly the probe's method and path
  - a route the app's binding allows

  The request takes the app's ordinary path through the gateway: the injected credential, the integration's and binding's limits, and the app's daily usage. It bypasses the response cache. The response body is discarded, and a receipt is written only when the status equals the probe's expected status.
- **Receipt:** There is one immutable receipt per request attempt, integration and binding revision. It records the method, path and status. The revision is a SHA-256 over the facts the probe exercised:
  - the integration row
  - the app binding row
  - the account plan
  - the sealed primary credential and its update time
  - every key in the integration's key pool
  - the probe policy

  Insertion requires an enforced binding context, a live qualification lease, the current revision and the current probe policy. Any change to those facts takes effect immediately: the receipt stops counting, and the worker probes again while the lease is live. Downgrade refuses to discard receipts.
- **Activation:** Each customer binding of a candidate's app is handled as follows:
  - A binding is skipped, as binding verification skips it, when its integration is disabled, does not use managed auth, or has no allowed methods or path prefixes.
  - Any other binding without a customer-sealed credential and a probe policy blocks activation.
  - Every other binding needs a receipt for the current revision.

  The receipt's age is limited by the policy's maximum verification age. `environment_binding_catalog_supported` still blocks operator app attachments. Those integrations have no probe policies, and their credentials come from outboundd's environment, which no revision can capture.
- **Limits:**
  - This proves that the gateway can reach the provider with the platform's credential under the app's binding. It does not prove that candidate code calls the integration.
  - A key pool is exercised through the gateway's ordinary key selection, so one probe covers one key, as a runtime probe does.
  - Probe requests count against the binding's daily limit and are billed by the provider like any app call.
  - Native KVM acceptance and qualification dispatch remain pending.
- **Validation:** Portable memory and PostgreSQL tests cover:
  - activation with a current receipt
  - missing receipts and missing probe policies
  - revision drift after recording
  - receipt immutability
  - skipped disabled integrations
  - blocked operator attachments

  outbound tests cover the principal's admission rules and the worker's receipt on the expected status only.
