# ADR-147 · Request activity reporting for short idle timeouts

- **Status:** accepted
- **Date:** 2026-09-05
- **Decision:** gatewayd-internal flushes buffered instance activity every two
  seconds, superseding the fifteen-second cadence in specification §4.1.

## Context

Apps may configure a ten-second idle timeout. The gateway previously buffered
successful requests for up to fifteen seconds before reporting them to schedd.
A reaper tick could therefore observe an expired timestamp while the instance
was serving continuous traffic. An SSD test reproduced this: the background
instance began parking 13.5 seconds after wake despite ongoing successful
requests, causing retries, extra wakes and failed requests.

Request analytics sent to apid is a separate channel. Idle decisions use
schedd's ReportActivity RPC and its instance timestamps.

## Decision

Use a two-second production flush interval. Continue coalescing the newest
timestamp and total request-count delta per instance, and group reports by
the owning scheduler. The gateway request path remains free of database writes;
schedd remains the only writer to instances. Idle timeout values, admission,
and the reaper's idle criteria do not change.

The interval leaves eight seconds between normal reporting lag and the minimum
idle timeout. It does not guarantee liveness during a prolonged reporting outage.

## Consequences and validation

Continuously active instances with the minimum idle timeout no longer appear
idle between healthy reports. Idle instances remain eligible after traffic
stops. Nonempty report batches may run up to 7.5 times as often; request volume
still coalesces into one touch per instance per batch, and empty flushes do not
make RPCs. Observe scheduler/database load during rollout.

A deterministic regression combines the production activity sink and cadence
with the real idle selector at all ten phases of its ten-second tick. The old
cadence parks active guests in eight phases. Canary validation also exercises
continuous traffic alongside snapshot restores and checks background errors.

Increasing the test app's idle timeout would hide the supported short-timeout
failure. Delaying all idle decisions by fifteen seconds would change the
configured timeout instead of correcting the reporting lag.
