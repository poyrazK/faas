# ADR-246: Revision-scoped CPU autoscaling targets

- **Status:** proposed
- **Date:** 2026-09-25
- **Context:** The app's CPU utilization target is evaluated across all live instances and app-level scale-out may select a different revision from the one reporting high CPU. During canaries or dark deploys, revisions can have different resource profiles and should be able to scale independently.
- **Decision:** Add an optional `cpu_utilization_target_pct` to deployment creation. Omission inherits the app's CPU target; explicit zero disables CPU-based scale-up for that revision; values from 1 through 100 select a revision target. If any live deployment has an explicit override, CPU decisions are evaluated per live deployment and admissions target that deployment. RPS scaling remains app-wide.
- **Limits:** The app/plan max remains the aggregate cap. A deployment's `max_instances` adds a narrower cap, and sibling revisions' existing instances are subtracted from app headroom before a revision can scale. CPU-target plan eligibility remains Pro/Scale.
- **Persistence and telemetry:** Store the nullable override on `deployments`; include `DeploymentID` in per-instance stats and select CPU samples by `(app_id, deployment_id)`. Historical rows and omitted request values inherit without backfill.
- **Consequences:** A hot canary can scale without warming an unrelated stable revision, and a revision can opt out while siblings inherit the app setting. The scheduler remains the final authority for app and deployment admission caps.
- **Rejected alternatives:** Temporarily change the app target during a rollout, which mutates policy shared by every revision; duplicate all scaling-policy axes on deployments, which expands the contract beyond the demonstrated CPU gap; infer a target from deployment CPU resources, which would make resource shape silently alter autoscaling behavior.
