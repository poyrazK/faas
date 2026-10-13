# ADR-996: Longer Run deadlines for general compute

- **Status:** accepted
- **Date:** 2026-10-11
- **Relates to:** ADR-171 (disposable one-shot executions), ADR-986 (Runs billing)
- **Decision:** Raise the admission-to-result deadline of a disposable Run to 1 minute on Hobby, 5 minutes on Pro and 15 minutes on Scale (previously 10 s / 30 s / 30 s). `api.ExecutionTimeoutHardMaxMS` becomes 900,000 ms, and the `executions.timeout_ms` CHECK follows it. The default stays 5 s. Memory, CPU, output, network (`none`) and concurrency limits are unchanged.
- **Why:** A 30-second ceiling only fits snippets. Batch transforms, report generation, data jobs with the `python-data-v1` profile, and test or build steps in a workflow need minutes. With Runs billed per running second (ADR-986), a longer Run costs what it uses, like any other compute.
- **Consequences:**
  - Plan concurrency still caps simultaneous Runs, so the longest burst one account can hold is 20 Runs × 15 minutes on Scale.
  - The scheduler lease is renewed every few seconds during a Run, and the durable deadline still includes queue and restore time.
  - `gregale run --wait` now waits up to 20 minutes by default, so a client does not give up before a Scale-length Run ends.
  - guest-init validates the hard maximum, so longer deadlines need the new guest-init. imaged patches it into every base at startup. That changes the base identity, and stale runtime snapshots are replaced by new captures.
- **Rejected alternatives:**
  - Unbounded Runs were rejected because long-running work belongs in Jobs or services, which have their own retry, scheduling and placement contracts.
  - Keeping 30 s and pointing users at Jobs was rejected because Jobs need a built image, while a Run is source plus input. That is the general "run this code" primitive.
