# otlp_exporter Ansible role

This role prepares the optional OpenTelemetry exporters in the faas daemons to
use an operator-owned environment file. The release-managed daemon units load
that file directly. The role does not install or run an OpenTelemetry Collector
and it never writes exporter credentials.

The role installs `/etc/faas/otel.env.example` and validates `/etc/faas/otel.env`
when present. The latter must be a `root:root` `0400` file; systemd loads it
into each daemon before switching to the unprivileged service user. The file is
optional, so local Prometheus/Grafana and the gateway trace ring remain the
default fallback.

Use a Collector as the vendor-neutral boundary. The Collector can fan out
OTLP traces and metrics to Datadog, Tempo, or another backend without adding a
backend SDK or credential format to the faas daemons. See
`docs/ops/otlp-export.md` for the configuration and verification procedure.
