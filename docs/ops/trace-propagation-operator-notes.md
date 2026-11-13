# Trace propagation: operator notes

Moved out of the customer page `docs/runtimes/trace-propagation.md`
(ADR-20261028200304). The tracing contract is `docs/faas_implementation_spec.md` §16.

## Settings

`FAAS_OTEL_SPANS_WRITER_ENABLED=false` disables both the customer OTLP writer
and the platform-owned service-proxy span retention path;
`FAAS_OTEL_FLUSH_INTERVAL` controls their coalescing cadence (30 seconds by
default). Platform spans include `sched.wake`, `vmmd.create_*`, `guest.resume`
and `guest.readiness`; outbound integration spans come from `outboundd` and are
routed through apid's spans-writer.

## Platform trace query (observer access)

If you have observer access to the box, `GET /v1/traces/{trace_id}`
returns the gatewayd-public platform span tree retained by that
instance's bounded in-memory ring (24 hours or 100k entries, whichever
comes first). It is not a durable or fleet-wide trace store. The shape
is the [`Trace` schema](../../api/openapi.yaml) — every platform span carries
`trace_id`, `span_id`, `parent_span_id`, `name`, `start_time`,
`end_time`, `status`, and an `attributes` map (`app_id`,
`deployment_id`, `instance_id`, etc.).

Customer-created spans require OTLP export and are inspected in the
configured collector. The customer OTLP ingest path summarizes those
spans into request telemetry rather than adding them to this local ring.

```bash
curl -sH "X-Faas-Trace-Auth: $OBSERVER_TOKEN" \
  https://faas.example.com/v1/traces/4bf92f3577b34da6a3ce929d0e0e4736 | jq
```

