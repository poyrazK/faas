# ADR-20261031180000: PR previews apply plan admission and keep the tenant and security gates

- **Status:** accepted
- **Date:** 2026-10-31
- **Amends:** ADR-095 (PR preview environments), ADR-20261023120000 (Compose resources), ADR-20261031120000 (new services, skipped heads, production protocols)
- **Decision:** A seventh code review of PR previews found these gaps; this ADR fixes them.
  1. **A PR head is held to the plan a project deploy applies.** A project deploy refuses workloads the plan does not allow before writing them: declared memory above the plan's per-app RAM, replicas above its concurrency, image process options, lifecycle settings (stop grace, startup deadline, retries, profiling, tracing) and platform-tenant-required apps. The preview path skipped that admission and wrote the head's declared resources onto the preview. schedd clamps concurrency and worker replicas to the plan, but boots a VM with the app's RAM and the meter bills it, so a Free account's preview could run with many gigabytes. githubd now applies the same admission (`reconcile.WorkloadPlanAdmissionReasons`) to every workload the preview builds, before anything is reserved; a refusal is a setup failure reported on the PR, as a merge of that head would be refused.
  2. **A preview keeps the production app's tenant-only gate and security posture policy.** `platform_tenant_required` makes the gateway refuse traffic without verified tenant attribution; `security_policy` makes deploys refuse high-severity posture findings. Neither was copied to previews, and a preview never took them from its head either, so a tenant-only app's preview answered anonymous requests. Webhook and API previews now copy both when created, every new head refreshes them, and `sync_preview_access_gates` carries them, for the previews of an app and for the services its PR environments add. A backfill applies them to existing previews.
- **Why:** A pull request must not get more resources, or fewer access controls, than the merge of the same code would.
- **Consequences:**
  - One migration replaces `sync_preview_access_gates()` and its trigger (two more columns) and backfills existing previews in batches. Older writers leave the columns alone.
  - A PR whose head the plan refuses gets a failed `gregale-preview` check that names the limit, instead of a preview.
  - The quota insert now writes `security_policy`.
- **Rejected alternatives:**
  - **Clamp a preview's RAM to the plan instead of refusing the head.** The preview would run a configuration production refuses, and the PR would only learn about it at merge.
  - **Let the PR head declare the tenant gate.** The gate is the production app's access control; letting PR code drop it defeats it on a public host.
