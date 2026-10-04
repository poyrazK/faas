# Node HTTP admission

Implementation: ADR-570. Native Linux KVM, process-fence and leak acceptance
remains pending; this is not a completed release capability.

The VM node enforces the existing plan cap from the instance's trusted wake
plan: Free 4, Hobby 5, Pro 25, Scale 80. It counts forwarded exchanges across
gateway processes. No request header can raise it. Upgrading a plan affects
new instances; an already-live VM reports its actual wake plan and cap.

| Path | Node HTTP cap |
| --- | --- |
| Public or declared-service `ForwardHTTPStream`, H1/H2C/gRPC | One permit for the full forwarded exchange |
| `ForwardRawStream` Upgrade | Same gate before handshake; held for the session |
| Managed synthetic HTTP routed through those RPCs | Same gate |
| Public cache/fixed edge response | No guest forwarding permit needed |
| TCP service tunnel, background guest work | Outside this HTTP cap |
| Builder, job, app task, disposable execution VM | Not an ordinary routed HTTP listener |

Gateway queue estimates remain useful before the final node decision. A
racing full node refuses immediately with `concurrency_throttled`/429 and
`Retry-After: 1`; it does not enqueue unbounded work or replay the app request.
An unavailable owner or untrusted instance plan returns
`http_admission_unavailable`/503. Neither outcome executes the guest request.

Inspect `Stats.instances.http_admission_enforcement`: enabled, limit,
inflight, generation, retiring and plan. Absence means the peer does not
report this mechanism. Inflight includes cancelled requests whose bridge
cleanup has not completed; cancellation is not evidence of free capacity.

Persistent v2 bridge completion is acknowledged over its private socket.
If that acknowledgement is uncertain for five seconds, vmmd reaps the bridge
before releasing capacity. Sibling requests on that child can fail. An old
bridge socket that still accepts connections prevents startup; confirmed
dead sockets are removed. Managed deployments require the generated vmmd
unit's `KillMode=control-group` and Linux bridge parent-death/process-group
fences. Do not replace that unit with a process-only kill policy.

Local tests exercised all plan caps, cancellation retention, delayed release,
replacement fencing, lease recycling, bridge completion and stale socket
checks. Two separate HTTP forwarding processes, a replacement, one real gRPC
vmmd service and the reusable bridge binary preserved a node cap of four;
Upgrade refusal occurred before tunnelling. Guest networking and VM lifecycle
were fixtures. The full local fcvm and vmmdgrpc suites passed. Linux-only
parent-death/compatibility-child tests, actual KVM park/restore/migration,
daemon restart, production load and leak evidence remain required.

A further process test runs the production public `Handler` and managed
`ServiceProxy`, their HTTP/raw forwarders, the vmmd gRPC service, fcvm admission
owner and reusable bridge. Four mixed public/managed requests, including bodies
streaming after response headers, fill the trusted Free cap. The fixture app
advertises Scale and requests forge instance, plan and cap headers; capacity
remains four. Both gateway processes and a replacement preserve structured
429/503 refusals before guest execution, with one forwarding RPC per refusal,
no placement eviction and no observed replay. Upgrade refusals use the same
gate. Cancellation is followed through bridge cleanup before fresh public and
managed work succeeds.

This test supplies app policy, caller identity, authorization, warm placement,
namespace and VM startup fixtures. It does not run configured gateway daemons,
stored declared policies, native guest networking or deployed load acceptance.

A configured gateway test now connects those components: two `runWithDeps`
processes and a replacement read actual Postgres public routing, account/app
policy, declared bindings, source identity and reliability snapshots, then dial
a separate vmmd process over its Unix socket. The production node owner and
reusable bridge preserve the same four-exchange cap despite the account's Pro
plan and forged request headers. HTTP and Upgrade full/untrusted refusals make
one RPC each and no guest call. Mixed public/managed streaming requests retain
capacity until cancellation cleanup; both routes subsequently serve new work.
A forged managed caller is refused before the node receives an RPC.

Warm placement, listener/source-address translation, VM startup, namespace
selection and the guest HTTP server remain fixtures. This case uses configured
compute gateway startup; outer discovery, the separate public edge daemon,
cross-host authentication and native/deployed acceptance remain separate.
