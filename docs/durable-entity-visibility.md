# Durable entity visibility

Read one owned entity without invoking its handler, acquiring ownership or changing
its recovery metadata:

```sh
gregale entities inspect --app my-app --namespace counters --key customer:123
gregale entities inspect --app my-app --namespace sessions --key run:1 --environment production --tenant <owned-tenant-uuid> --json
gregale entities watch --app my-app --namespace reservations --key seat:1 --timeout 5m --interval 5s --json
```

Selectors identify one exact scope; omitted environment uses the existing API
default. These commands use the authenticated metadata inspection endpoint and
its existing app ownership checks. They do not list entities or read application
payloads. Missing entities, disabled previews and unavailable inspections return
errors rather than fabricated empty or healthy reports.

Reports include the committed version, commitment flag, recovery revision,
alarm deadline/attempts/backoff, pending intent count, head ID and transport
observation. Diagnostics distinguish scheduled, due, backing-off and exhausted
work. Due means due by the CLI's clock; it does not prove a stuck worker.

Transport success with a still-pending entity head is possible: webhook acceptance
and entity acknowledgement are separate steps. Unknown history may be unavailable
or pruned and never establishes that no delivery occurred. A failed/dead delivery
observation is also distinct from an exhausted entity retry budget.

The watch command emits its first report and changes only, including transitions
caused by a deadline passing without a new state version. JSON output is newline-
delimited reports. The default window is five minutes, capped at one hour; poll
intervals range from one second to one minute. Completion of the window during
poll waiting succeeds; read errors, including timed-out reads, fail. Interrupting
returns exit 130. Watch never retries an alarm or outgoing intent automatically.

An exhausted alarm calls for checking the handler failure; an exhausted outbox
head calls for checking webhook configuration and retained delivery history. Use
the existing explicit fenced retry API only after investigating and obtaining a
fresh inspection. The revision is an opaque comparison value, not ownership
permission. These commands supply no mutation command.

## Node SDK

```ts
const report = await entityHandle.observe();
console.log(report.alarm_status, report.outbox_status, report.findings);
```

`observeDurableEntity(selectors)` offers the same metadata observation independently
of a handle. `summarizeDurableEntity(inspection, clock)` is a pure diagnostic helper
for an already captured inspection and an optional explicit clock. Reports retain
a copied inspection; neither helper requests application data or invokes code.
Invoke results already expose `version` and `replayed`; retain the stable request
ID and result for replay diagnostics. Visibility does not replay an invocation to
find its outcome.

## Observation limits

The current API does not expose ownership/lease status, replay receipt history,
application state, handler error details or a fleet-wide entity inventory. Head
delivery is a later transport read, not atomic with entity metadata. Reports state
these limits explicitly. This feature adds presentation and diagnostics over the
existing read-only API; it does not change storage or production feature gates.

CI tests cover interpretation, read-only scope routing, changed-only watch output,
error handling and SDK copy isolation. Local tests/builds remain deferred to the
acceptance agent per the implementation instructions.
