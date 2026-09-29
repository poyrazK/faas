# Total ordinary-HTTP deadline

ADR-375 implementation; complete-path acceptance is pending.

The existing `kind=budget` action accepts:

```json
{"budget_ms": 1000, "total_deadline_ms": 3000}
```

`budget_ms` remains the execution allowance after upload, wake, routing and
capacity admission. `total_deadline_ms` includes elapsed public ingress time,
upload, auth, cache/route lookup, queueing, wake, retry attempts and the ordinary
response exchange. The plan ceiling caps both. Zero or omission leaves the
total deadline unset. An execution override cannot increase the total deadline.
CLI create/update accepts `--total-deadline-ms`; retain `--budget-ms` too.

The private compute listener consumes a timestamp replaced by the public proxy.
Customer timestamp claims are overwritten and the transport header is removed
before policy/guest forwarding. A total rule is matched on the public route
after owner resolution and pinned through wake and execution. Elapsed policy
lookup time is charged once the rule is resolved; lookup still has the existing
platform envelope and store timeouts before the customer policy is known.

Before response commitment, expiry returns 504 with
`code: request_budget_exceeded`. Expiry during upload closes the original body,
removes any spool file and prevents wake admission. Shared wake work may remain
bounded for other waiters. Ordinary responses end on expiry after commitment;
streaming and upgrades switch to their existing idle/session bounds after
successful response headers. Client cancellation remains effective.

Nested managed-service deadline transport, cross-node clock/transport failure
acceptance, overload/retry/stream integration and rollout evidence remain
required. Detached work, arbitrary guest sockets and background computation
after a disconnect are outside this request deadline.
