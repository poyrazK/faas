# ADR-20261026090000: PR preview environments for general availability

- **Status:** accepted
- **Date:** 2026-10-26
- **Amends:** ADR-095 (PR preview environments), ADR-310 (preview quotas), ADR-312 (preview freshness), ADR-20261025093331 (whole-project previews)
- **Decision:**
  1. **The allowance counts environments.** `Limits.PreviewApps` caps open PR preview *environments*: the open previews of one project and pull request (a legacy preview without a project is its own environment). One environment holds at most `Limits.DeployedApps` workloads, as many as the plan can deploy. A closed PR's preview keeps serving through its 24-hour grace but holds no allowance; reopening the PR takes one again, and fails like a new preview when none is free. Admission errors name the limit that tripped (`preview_apps` or `preview_workloads`).
  2. **Destroy tears down the environment.** `POST /v1/preview/{slug}/destroy`, `gregale preview destroy`, the dashboard Tear down action and the server-rendered previews page tear down every open preview of the named preview's project and pull request. A developer session stays an environment of one.
  3. **Every failure reaches the pull request.** A failure githubd cannot retry away is a *setup failure*: the PR head's project cannot be read, its dependency graph is invalid, the environment does not fit the plan, or apid refuses the build (deploy rate limit, source too large, invalid manifest, an account that cannot deploy). githubd records it on the PR's preview set (`pr_preview_sets.setup_error`), moving the set to the failed head. It fails the `gregale-preview` check, says so in the status comment and completes the delivery. A delivery that exhausts its retries on retryable errors is reported the same way. The recorded error fails the set everywhere it is read: the check, `GET /v1/preview/{slug}/environment`, `gregale preview wait` and the dashboard. The next head clears it. Text written to a PR omits account billing and abuse state, because the PR may be public.
  4. **Previews keep their production app's gates.** Reserving a PR head refreshes the access and egress gates and the visibility of every reused preview from its production app. A trigger copies a production app's later gate changes onto its live previews, and a migration backfills previews created before gates were copied (#5290). A service the PR introduces takes the bound app's gates and the visibility production would give it, so a service without ports stays internal.
  5. **Links resolve.** PR comments, Check Runs and GitHub deployments link only to dashboard routes that exist: the app page, its `?tab=Deployments&deployment=<id>` and `?tab=Logs` views. The app page's menu tears a preview down. The server-rendered dashboard gains the `POST /dashboard/apps/{slug}/preview/{previewSlug}/destroy` route its previews page already posted to.
  6. **Housekeeping.**
     - The status comment upsert adopts only a bot comment attributed to the Gregale App and reads up to 1,000 comments.
     - A preview reports to the GitHub environment `preview/<production app>`, not one environment per pull request.
     - A torn-down preview's GitHub deployment goes inactive and its comment says it is gone.
     - A PR push that changes only ignored paths keeps a live preview serving its recorded head instead of rebuilding.
     - Fork refusals are written only for bound repositories with previews on.
     - Parked production apps can be previewed.
     - The CLI accepts preview slugs longer than 40 characters.
     - A janitor failure pages through `FaasPreviewJanitorFailing`.
- **Why:** A GA review found these gaps:
  - The documented destroy link and deployment links returned 404.
  - A Free account could not preview a second PR for 24 hours after merging the first.
  - A default (`project` policy) multi-service repository could not be previewed on Hobby or Pro.
  - Pre-build failures left the PR saying "queued" forever.
  - Gate changes never reached open previews, and a PR-only internal service was exposed publicly.
  - Destroying a preview left its siblings holding the allowance.
- **Consequences:**
  - The plan table's numbers are unchanged; their meaning for previews widens. Some accounts that were refused before now get previews, and a large environment costs more compute while it runs. Previews still scale to zero, and the lease janitor bounds their lifetime.
  - Migration `20261025224818182` adds `pr_preview_sets.setup_error`, the gate-sync and teardown triggers, and the gate backfill. It is expand-only; an older githubd neither writes nor reads the column.
  - A preview's gates are no longer independently editable while its production app changes them; previews follow the production policy.
  - Per-workload rebuild skipping inside a set is not part of this decision: a set is ready only when every member is live at the exact head (ADR-312), so a push that changes source still rebuilds every member.
- **Rejected alternatives:**
  - **Raise `PreviewApps`.** Any fixed per-workload number still fails a larger repository, and it would price review environments by service count.
  - **Retry setup failures.** They repeat until the source or plan changes, and they burn the hourly deploy allowance and GitHub API quota while the PR shows nothing.
  - **Add dashboard routes to faas-web for the old link shapes.** That repository can lag the API. Links to the app page work with every dashboard release.

## Amendment (2026-10-26): GA promotion

`pr-previews` moves from `beta` to `ga` in `pkg/productcap/catalog.json`.
Its platform flag default is unchanged, because beta and GA flags are both on
for every account. Two changes come with the promotion:

- The developer preview API (`POST /v1/apps/{slug}/previews`,
  `gregale preview create --pr-number`) no longer copies the production app's
  environment values into the preview. It keeps only the platform-owned
  service binding URLs, as the GitHub webhook path already did (ADR-310).
  Secrets were never copied, because they belong to an app row. Previews
  created before this change keep their values until they are torn down.
- The catalog's acceptance evidence is now
  `cmd/e2e/github_webhook_deploy_e2e_test.go::TestE2E_GitHubWebhookEnqueuesDeployments`.
  It runs a real apid and githubd's production webhook path against
  PostgreSQL:
  - open queues the preview build, its GitHub status sync and the PR comment;
  - close starts the grace window;
  - apid's janitor tears the environment down and queues the final GitHub
    status sync.

  Only the GitHub HTTP writes are recorded instead of sent. Their wire format
  is covered by the `pkg/githubd` and `cmd/githubd` tests.
