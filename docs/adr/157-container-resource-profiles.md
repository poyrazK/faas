# ADR-157 · Named container resource profiles

- **Status:** accepted
- **Date:** 2026-09-05
- **Decision:** Expose a closed set of named app resource profiles: `micro` (128 MB / 250 mCPU), `small` (256 MB / 500 mCPU), `medium` (512 MB / 1000 mCPU), `large` (768 MB / 1000 mCPU), and `xlarge` (1024 MB / 1000 mCPU). The API resolves a profile into the existing `ram_mb` and `cpu_millicores` controls before plan validation and cgroup enforcement. Explicit memory or CPU values may accompany a profile only when they agree with it.
- **Why:** Raw memory and CPU knobs are difficult to standardize across deployments and make capacity planning opaque. Named profiles give container workloads a stable, reviewable shape while preserving the existing x86 guest and cgroup implementation.
- **Consequences:** Create and patch requests accept `resource_profile`; app responses report the matching profile when a configured shape is one of the named profiles; the CLI exposes `--profile` on app updates and scaling. Custom RAM/CPU combinations remain supported and omit the profile field. Profile values still pass through the account plan limits, so a profile that exceeds a plan is rejected before persistence.
- **Rejected alternatives:** Persisting a second profile column would duplicate the already authoritative RAM/CPU fields and create drift on custom updates. Per-container CPU and ephemeral-disk quotas remain follow-up work once the named app shape contract is in use.
