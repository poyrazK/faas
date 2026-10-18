# FaasDBNotifyHub

Covers `FaasDBNotifyHubReconnects`, `FaasDBNotifyHubDropped` and
`FaasDBNotifyHubDisabled`. Severity: warn. Spec §12, ADR-190 §4.

Each daemon holds one shared `LISTEN` connection (the notify hub) and fans
`pg_notify` payloads out to its subscribers. Work driven by notifications is
also recorded in durable tables and picked up by safety ticks, so none of these
alerts means lost work; they mean slower or more expensive delivery.

## FaasDBNotifyHubReconnects

The hub's connection dropped more than once in 5 minutes. Check for Postgres
restarts, failovers, pgbouncer or firewall idle-connection reaping, and
`journalctl -u faas-<daemon>` for the disconnect reason.

## FaasDBNotifyHubDropped

A subscriber is not draining its fan-out buffer for one `channel`, so the hub
drops notifications for it. Find the consumer of that channel in the daemon and
check whether its handler is blocked (slow query, held lock, saturated pool).
Consumers catch up from the durable table, later than they would from the
notification.

## FaasDBNotifyHubDisabled

The daemon has subscribers but no hub connection: `FAAS_DB_NOTIFY_HUB=0` is
set and each subscriber holds its own `LISTEN` connection. This is a supported
mode (`db.DaemonMaxConnectionsNotifyHubDisabled` sizes the pool for it), but it
spends the pre-hub connection count against the fleet's `max_connections`.
Unset the variable unless the hub was disabled deliberately for an incident.
