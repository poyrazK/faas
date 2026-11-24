# Verify restored HTTP routes during project rollback

Status: Accepted

Project rollback can opt into HTTP health verification with `--verify`. Admission pins the concrete GET or HEAD probes configured for targeted HTTP services. Route-health probes take precedence over explicitly configured release probe paths. At least one targeted service needs a probe to start a verified rollback.

After a service cuts over, the worker waits for gateway acknowledgements of that route generation. Existing deployment-scoped probes check the exact restored revision. Every pinned probe must return an attributed 2xx to release the next cutover. The project receipt records probe progress and verdicts. PostgreSQL validates the earlier serving routes during cutover authorization and requires the health-aware authorization token for verified rollbacks.

An attributed non-2xx pauses the operation. Unavailable evidence can retry until the per-service one-minute deadline or the shared preparation deadline. The worker releases unused preparations while retaining the failed checkpoint. An operator can resume after repairing the restored service. Resume rechecks that service before the remaining targets can cut over. The last configured HTTP service also verifies before the operation completes.
