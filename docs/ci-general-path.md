# General-path testing gate

The general path is the customer request path that does not require KVM or a
Firecracker guest in the test runner. It is complementary to the native
acceptance suite: the native suite owns guest boot/build behavior, while this
gate owns the control-plane and gateway wiring around a ready instance.

The focused gate is:

```sh
DATABASE_URL=postgres://... make e2e-general
```

The focused target fails if Postgres is unreachable; it must not produce a
green no-op run.

It boots the real `apid`, `schedd`, and `gatewayd-internal` processes against a
real migrated Postgres schema. A fake VMMD listens on the same Unix-socket
gRPC boundary used in production and implements `ForwardHTTPStream` plus the
heartbeat response. The tests therefore exercise process configuration,
Postgres state, `pg_notify` route invalidation, node-client dialing, request
framing, and customer-visible failure mapping without needing `/dev/kvm`.

The current contract is intentionally small and high-signal:

| Test | Regression class |
| --- | --- |
| `TestE2E_NormalPath_RealGatewayBridgeAndRedeployRefresh` | live deployment cutover and route-cache refresh |
| `TestE2E_NormalPath_ForwardsHTTPContract` | method, path, chunked body, app-protocol metadata, and request-header filtering |
| `TestE2E_NormalPath_UnknownHostDoesNotReachBridge` | unresolved host returns 404 without cross-tenant bridge traffic |
| `TestE2E_NormalPath_HostNormalizationPreservesRoute` | host casing and explicit port still select the same app |
| `TestE2E_NormalPath_RequireAuthnBlocksUnauthenticatedTraffic` | per-app auth denies missing/foreign keys before bridge and allows the owner key |
| `TestE2E_NormalPath_ReassemblesResponseChunks` | multi-frame VMMD response reassembly |
| `TestE2E_NormalPath_NoContentResponsePreservesEmptyBody` | empty 204 response framing and response metadata |
| `TestE2E_NormalPath_HEADSuppressesResponseBody` | HEAD response-body suppression with header preservation |
| `TestE2E_NormalPath_PreservesResponseTrailers` | post-body trailer metadata survives the bridge |
| `TestE2E_NormalPath_AsyncInvokeUsesRealGatewayBridge` | durable async dispatch through schedd and the real gateway synth bridge |
| `TestE2E_NormalPath_AsyncInvokeGuestFailureIsTerminal` | guest 4xx becomes a terminal failed invocation with its result retained |
| `TestE2E_NormalPath_AsyncInvokeRetriesGuestServerError` | guest 503 is retryable and a later success completes the same invocation |
| `TestE2E_NormalPath_AsyncIdempotencyDoesNotDuplicateWork` | retrying an async request reuses one durable invocation row |
| `TestE2E_NormalPath_SyncInvokeReturnsRealBridgeResult` | synchronous long-poll completion and result projection |
| `TestE2E_NormalPath_QueueUsesRealGatewayBridge` | queue send/receive delivery through the real gateway synth bridge |
| `TestE2E_NormalPath_QueueDeliversMultipleMessagesExactlyOnce` | multiple queue payloads are delivered without loss or immediate redelivery |
| `TestE2E_NormalPath_QueueFailureExhaustsIntoDeadLetter` | transient guest failures stop at the plan budget and remain inspectable |
| `TestE2E_NormalPath_DelayedTaskWaitsThenUsesRealGatewayBridge` | delayed scheduling, due-time dispatch, and result persistence through the real synth bridge |
| `TestE2E_NormalPath_CancelledDelayedTaskNeverReachesBridge` | pending delayed-task cancellation prevents later bridge delivery |
| `TestE2E_NormalPath_AsyncInvokeRetriesTransientBridgeFailure` | claimed invocation retry after a transient VMMD outage |
| `TestE2E_NormalPath_GuestStatusAndHeadersPassThrough` | guest non-2xx status, response headers, and body passthrough |
| `TestE2E_NormalPath_GuestServerErrorPassesThrough` | guest 5xx remains distinguishable from a gateway/VMMD outage |
| `TestE2E_NormalPath_ProxyActivityBecomesDurable` | successful proxy activity reaches schedd and persists request count/last-seen state, including a burst |
| `TestE2E_NormalPath_GuestFailureDoesNotRefreshActivity` | guest 4xx does not increment request count or refresh durable last-seen state |
| `TestE2E_NormalPath_BridgeUnavailableThenRecovers` | VMMD outage maps to 503 and does not poison the route |
| `TestE2E_NormalPath_StoppedInstanceInvalidatesRoute` | stopped instance state invalidates a previously cached route |
| `TestE2E_NormalPath_GatewayRestartReloadsDurableRoute` | gateway restart rehydrates routing from Postgres |

The tests seed a deployment after the source/build/image pipeline has produced
its durable state. They must not become a second KVM suite. Add a scenario here
when the bug is in daemon wiring, durable state propagation, routing, bridge
framing, or restart/recovery behavior; add it to the native suite when the bug
requires a real guest, jail, network namespace, image, or Firecracker lifecycle.

All files under `cmd/e2e` are discovered by `scripts/ci/e2eshard`, so the
`TestE2E_NormalPath_` cases are included automatically in the existing PR E2E
shards. `make e2e-general` is the fast local feedback loop for this group; it
does not replace the full sharded gate.

The next general-path gaps are intentionally tracked rather than hidden:

- client disconnect/cancellation while a request body or response stream is
  active, including bridge cleanup and no leaked goroutines;
- concurrent in-flight requests with independent headers, bodies, and trace
  context, plus bounded backpressure behavior;
- response-side RFC hop-by-hop header filtering and HTTP/2/gRPC app-protocol
  acceptance through the real daemon bridge.
