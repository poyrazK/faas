# ADR-20261017201005 · Rehearsal network containment

- **Status:** accepted; operator enrollment gate remains off pending native qualification
- **Date:** 2026-10-17
- **Decision:** Add opt-in `actions: {webhooks: capture, network: contained}`. Retain an immutable network policy on each fresh run member. Enforce a separate default-drop forward hook before app execution and recheck it before resident resume. Allow published app ingress and canonical private HTTP calls between active, contained members of the same run.
- **Why:** An application can bypass managed webhook capture using its own socket. Ordinary egress allowlists, private networks, operator exceptions and DNS-resolved address sets intentionally permit paths unsuitable for a rehearsal boundary.
- **Consequences:** Unavailable policy storage refuses boot/resume. Old runtime nodes reject dedicated rehearsal boot methods. Public DNS and non-run proxy targets are refused. Unsupported dependencies fail closed. Enrollment is disabled until the complete serving fleet is upgraded and native acceptance passes.
- **Rejected alternatives:** A fake deny CIDR, clearing extra egress ports or using a code-review warning cannot enforce isolation. Qualification networking has different ingress and graph authority. Publishing a receipt after unrestricted execution cannot repair an escaped action.

## Supported scope

Version 1 contains serving app VMs, including workers exposed as run-member HTTP
services. Normal inbound requests and their replies remain possible. Guest
outbound traffic is limited to the node bridge's canonical HTTP service proxy
on TCP 10081 and its private service-name DNS resolver on TCP/UDP 5354. Private
HTTP authorization requires both retained run enrollment and active membership,
matching account and run, and unexpired preview apps. Every allowed target is
itself contained. Deleting membership cannot enable production fallback.

Contained DNS traffic addressed to the ordinary bridge resolver on port 53 is
transparently DNATed inside the namespace to the dedicated private-only listener
on port 5354. No published caller identity or readiness transition is needed to
classify these requests. Ordinary startup DNS retains its existing forwarding
behavior. Contained DNS callers can query only `<workload>.svc.gregale`; the resolver
answers the bridge address and never asks a public upstream or returns a direct
service address. Binding aliases, the legacy proxy port, bridge HTTPS, direct
service TCP, public addresses, operator exceptions and private-network routes
are outside this profile. IPv6 guest-initiated traffic has no exception.
Development tunnels are refused on private service calls and public app routes,
including after namespace cleanup; they cannot execute a contained service
outside its VM boundary. The host-mediated outbound relay, federated workload
identity and guest event publishing are also refused. These vsock channels
would otherwise bypass IP filtering or fan out into production consumers.
Read-only runtime metadata and lifecycle telemetry remain available.

Database and object-storage access require future verified, isolated adapters.
Email, payment and arbitrary HTTP requests are blocked, not simulated or
recorded as business intents. Managed webhook capture/comparison retains its
existing scope. This decision adds no claim that a blocked socket attempt was
captured or that asynchronous work has completely drained. Builds, separate job VMs and standalone Runs executions are outside the
serving-VM boundary. Deployment-attached app tasks,
managed application-standard native admission, live migration and GitOps
qualification execution are refused for contained app identities in this
version. The Docker scenario engine remains unsupported.

ADR-20261017201006 adds a distinct simulation-only relay capability for declared HTTP and
Resend fixtures. It does not reopen live forwarding or change this network
profile's direct traffic boundary. Its version-2 evidence scope separately
records supported relay attempts.

## Authority and lifecycle

Registration must precede any deployment, instance or managed work. All members
request the same policy. Its app/account/run/network fields are immutable in
PostgreSQL; ordinary rollback cannot remove containment with enrolled apps
remaining. Namespace pruning, expiry, sealing and soft deletion retain the
policy. Physical app/account purge remains the retention boundary.

The scheduler reads retained policy at every ordinary boot dispatch, including
snapshot and paused restores. The typed client calls distinct
`CreateRehearsalColdBoot` / `CreateRehearsalFromSnapshot` methods, with no legacy
fallback. It requires the exact installed run ID in the runtime response and
destroys a runtime with a missing or mismatched acknowledgement. The scheduler
also checks this acknowledgement before accepting another VMM implementation.
New vmmd managers independently read authoritative policy on all app wakes,
including legacy RPCs that omit the field. A supplied field must match storage;
an unavailable authority cannot acknowledge containment. Ordinary app admission
with a configured authority refuses a missing app identity.

The namespace has an independent `inet gregale_rehearsal` forward hook at
priority -10 with policy drop. It precedes ordinary forward accepts. Only
connection replies, published DNAT ingress, canonical proxy and pinned DNS are
admitted. Private hops also require the fixed guest source address, preventing
source spoofing during ordinary-policy resets. DNS redirection lives in the same
independent table and survives those resets. The reply exception uses conntrack direction, so a generic established
connection rule cannot grandfather guest-originated connections. Ordinary
firewall resets never delete this table; a complete render rebuilds its rules
atomically. App egress, operator, DNS-gate and private-network updates cannot
widen it. A backend without atomic nft input transactions refuses contained
boot and resume; sequential command fallback cannot acknowledge this profile.
Rehearsal wakes bypass the unguarded prepared-network cache.

Snapshot restore and cold-boot fallback use this policy before releasing guest
execution. Resident resume, thaw, quiet resume and snapshot recovery resume
recheck the retained run and reinstall the guard. Native network operations
retain the original lease/generation authority. Native daemon recovery
quarantines surviving processes instead of adopting them as serving instances;
subsequent wakes allocate fresh guarded namespaces. The durable enrollment
therefore remains required after daemon restart.

## Rollout and evidence

Apply migrations, then upgrade schedd, every serving vmmd and every internal
gateway. Serving vmmd nodes need database-backed policy authority; internal
gateways need verified caller identity and policy reads. Keep
`FAAS_REHEARSAL_NETWORK_CONTAINMENT` unset on apid until the entire fleet has
passed qualification. Old scheduler/runtime pairs cannot interpret the new
policy; the operator gate is the rollout boundary for that combination.
Enabling enrollment during a partial upgrade is unsupported. Disabling the
enrollment gate later does not remove existing enforcement. Do not downgrade
scheduler, runtime or internal gateways to pre-containment binaries while
contained apps remain. Retire and physically purge those apps, or retain all
enforcing component versions throughout rollback.

Run `scripts/ci/rehearsal-network-acceptance.sh` on the dedicated native x86_64
Linux KVM host, with staged images, tenant bridge, free bridge ports 53/5354/10081 and
an operator-owned public TCP 80/443 probe endpoint. The packet test proves
IPv4/IPv6 denial despite ordinary accepts and after ordinary firewall resets.
The actual guest test establishes successful public control probes, then proves
that a contained guest cannot connect to either port while its mock private
HTTP dependency and private DNS remain reachable, while public DNS stays
refused, including after freeze/thaw. The native gate
runs `test-metal` and `leakcheck`. Full deployment, snapshot/resume and daemon
recovery acceptance must also pass under the current native lifecycle gates
before enabling a fleet. Portable tests and metal compilation do not substitute
for native qualification. No such qualification was available in this managed
workspace.
