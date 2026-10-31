<!--
Unreleased (ADR-20261022203523). This page is deliberately not listed in
docs/customer-pages.json, so it is not published. At launch, when
FAAS_PER_INSTANCE_CONCURRENCY_ACCOUNTS is "*": move the section below into
docs/scaling-policy.md before "## Scaling signals", add
`per_instance_concurrency` to that page's manifest example, add the CLI flag
to cmd/gregale/cli_meta.go and the usage lines, and delete this page.
-->

## Requests per instance

By default the gateway sends an instance up to your plan's concurrency per VM
(Free 4, Hobby 5, Pro 25, Scale 80) before it tries another one. A server that
handles fewer requests at once queues the rest itself, and the platform cannot
see that it is full. `gunicorn --workers 2`, for example, serves two requests
at a time. Tell the platform how many your server handles:

```yaml
scaling:
  max_instances: 3
  per_instance_concurrency: 2   # gunicorn --workers 2
```

```bash
gregale app APP_ID scale --per-instance-concurrency 2
```

With this setting each Gregale gateway sends an instance at most two
requests. The third concurrent request asks for another instance at once, up
to `max_instances`, and goes to whichever instance has a free slot first. When
every instance is full and the app is at its instance ceiling, requests wait
at the gateway in the warm queue.

- **Queue wait.** An app with `per_instance_concurrency` set waits up to 30
  seconds by default, because a full instance is normal for it.
  `max_queue_wait_ms` changes that wait, and `concurrency_overflow: drop`
  refuses the request at once. The request's own time budget (its request
  timeout) starts when it reaches an instance, not while it waits. A function
  with the 3-second default budget can therefore wait up to 30 seconds and
  then run for 3 seconds. Set `max_queue_wait_ms` if your callers need a
  shorter end-to-end bound.
- **Several gateways.** Requests reach your app through more than one gateway,
  and each gateway counts its own requests. For a moment an instance can
  hold up to the number of gateways times your value. Gregale watches every
  instance's real in-flight count and adds instances, up to `max_instances`,
  until none holds more than your value. Your server should tolerate a short
  backlog: keep its listen backlog at the default rather than refusing
  connections above the worker count.
- `0`, or leaving it out, uses the plan's concurrency per VM. The value can be
  at most that.
- Set it to what your server really serves at once: gunicorn `--workers`
  (times `--threads` for threaded workers), uvicorn `--workers`, puma workers
  × threads. Node, Go and asyncio servers usually handle many requests at once
  and do not need it.
- A lower value can run more instances for the same traffic, and each running
  instance is billed as usual. `max_instances` caps how many can run.
- Streaming responses and WebSocket connections hold their slot until they
  close.
- `gregale app APP_ID` shows the value as `requests per instance`.

This is different from `concurrent_requests` under `targets`, which is a
scaling target the scheduler samples over time. `per_instance_concurrency` is
a limit each gateway enforces on every request, and it also acts as a
`concurrent_requests` target unless you declare one. See
[ADR-20261022203523](adr/20261022203523-per-instance-request-concurrency.md).
