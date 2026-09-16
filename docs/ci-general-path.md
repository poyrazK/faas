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
| `TestE2E_NormalPath_AsyncInvokeGuestFailureIsTerminal` | guest 4xx becomes a terminal failed invocation with HTTP and guest detail retained |
| `TestE2E_NormalPath_AsyncInvokeRetriesGuestServerError` | guest 503 is retryable and a later success completes the same invocation |
| `TestE2E_NormalPath_AsyncIdempotencyDoesNotDuplicateWork` | retrying an async request reuses one durable invocation row |
| `TestE2E_NormalPath_SyncInvokeReturnsRealBridgeResult` | synchronous long-poll completion and result projection |
| `TestE2E_NormalPath_QueueUsesRealGatewayBridge` | queue send/receive delivery through the real gateway synth bridge |
| `TestE2E_NormalPath_QueueDeliversMultipleMessages` | multiple queue payloads are delivered without loss through the real bridge |
| `TestE2E_NormalPath_QueueFailureExhaustsIntoDeadLetter` | transient guest failures stop at the plan budget and remain inspectable |
| `TestE2E_NormalPath_DelayedTaskWaitsThenUsesRealGatewayBridge` | delayed scheduling, due-time dispatch, and result persistence through the real synth bridge |
| `TestE2E_NormalPath_CancelledDelayedTaskNeverReachesBridge` | pending delayed-task cancellation prevents later bridge delivery |
| `TestE2E_NormalPath_AsyncInvokeRetriesTransientBridgeFailure` | claimed invocation retry after a transient VMMD outage |
| `TestE2E_NormalPath_GuestStatusAndHeadersPassThrough` | guest non-2xx status, response headers, and body passthrough |
| `TestE2E_NormalPath_GuestServerErrorPassesThrough` | guest 5xx remains distinguishable from a gateway/VMMD outage |
| `TestE2E_NormalPath_ProxyActivityBecomesDurable` | successful proxy activity reaches schedd and persists request count/last-seen state, including a burst |
| `TestE2E_NormalPath_GuestFailureDoesNotRefreshActivity` | guest 4xx does not increment request count or refresh durable last-seen state |
| `TestE2E_NormalPath_BridgeUnavailableSurfaces503` | VMMD outage maps to a customer-visible 503 |
| `TestE2E_NormalPath_StoppedInstanceInvalidatesRoute` | stopped instance state invalidates a previously cached route |
| `TestE2E_NormalPath_GatewayRestartReloadsDurableRoute` | gateway restart rehydrates routing from Postgres |
| `TestE2E_NormalPath_GatewayRestartTerminatesInFlightResponse` | an in-flight bridge response is terminated on gateway restart and the fresh gateway serves the durable route |
| `TestE2E_NormalPath_ScheddRestartReclaimsAbandonedDispatch` | an expired async dispatch lease is reclaimed after schedd restart and completed exactly once |
| `TestE2E_NormalPath_CancelledUploadClosesBridge` | client cancellation during upload closes the real gateway-to-VMMD stream |
| `TestE2E_NormalPath_CancelledResponseClosesBridge` | client cancellation after response bytes begin closes the real gateway-to-VMMD stream |
| `TestE2E_NormalPath_ConcurrentRequestsPreserveIsolation` | concurrent bridge streams preserve each request's path, body, and customer headers |
| `TestE2E_NormalPath_PerInstanceBackpressureReleasesSlot` | a full per-instance concurrency slot holds the next request outside the bridge until release |
| `TestE2E_NormalPath_CancelledQueuedAdmissionReleasesCapacity` | canceling a queued request leaves it outside VMMD and preserves capacity for the next request |
| `TestE2E_NormalPath_QueuedAdmissionBudgetExpiryReturns504` | a platform-owned budget expiry while admission is queued returns the canonical 504 problem |
| `TestE2E_NormalPath_AppProtocolMatrix` | `http1`, `http2`, and `grpc` selectors reach VMMD; gRPC trailers remain trailers |
| `TestE2E_NormalPath_GuestHopByHopHeadersAreNotExposed` | guest connection-management headers are filtered at the customer response boundary |

The tests seed a deployment after the source/build/image pipeline has produced
its durable state. They must not become a second KVM suite. Add a scenario here
when the bug is in daemon wiring, durable state propagation, routing, bridge
framing, or restart/recovery behavior; add it to the native suite when the bug
requires a real guest, jail, network namespace, image, or Firecracker lifecycle.

All files under `cmd/e2e` are discovered by `scripts/ci/e2eshard`, so the
`TestE2E_NormalPath_` cases are included automatically in the existing PR E2E
shards. `make e2e-general` is the fast local feedback loop for this group; it
does not replace the full sharded gate.

The remaining general-path gaps are intentionally tracked rather than hidden:

- request-side RFC token-listed hop-by-hop filtering and real guest-side
  HTTP/2/gRPC framing, which require the native guest bridge.
