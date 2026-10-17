# ADR-1353: Bounded parallel binding verification

- **Status:** Accepted
- **Date:** 2026-10-14
- **Context:** Serial binding and graph member verification increases qualification time and can expire early evidence before later probes complete.
- **Decision:** Release-policy verification runs independent probes with a CLI concurrency budget, default 2, configurable from 1 to 8. Checked graph members share one budget; member orchestration never holds a probe slot. Slots cover admission, polling and cleanup. Ordinary server capacity and provider limits remain authoritative; concurrency grants no additional capacity.
- **Plans:** Validate structural blockers and all write/publication consent before starting work. Graph members retain the original reviewed snapshots, exact deployments, service targets and policy revisions. Every admission and completion reconfirms the snapshot. Reports retain plan order regardless of completion order.
- **Failure:** Stop further admissions on failure or interruption and join outstanding work before rendering. Cancel tasks and queue canaries admitted by this run on unsuccessful exits. Observing a preexisting queue canary does not grant cancellation ownership. Evidence deadlines are never extended.
- **Freshness:** Perform existing policy-aware preflight after member probes complete and recheck every original member snapshot after all graph members join. Activation retains the authoritative atomic graph, policy and freshness checks; CLI observations are not a traffic-write lock.
- **Scope:** CLI orchestration only; API, guest commands, SDKs, runtime admission and traffic routing are unchanged. Durable resume and parallel `--all` are future work.
- **Validation:** Shared cross-member limit, fail-fast admission and joined cleanup, owned versus reused cancellation, exact identity and policy/configuration drift regression tests, and two canaries whose serial duration exceeds a one-second evidence window while parallel qualification succeeds.
