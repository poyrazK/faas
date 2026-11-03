# ADR-20261029121500: Pull request CI runs only the light tier

- **Status:** accepted
- **Date:** 2026-10-29
- **Owner:** CI maintainers
- **Amends:** ADR-20261020160744 (decision 4, "Every pull request reports its blast radius")
- **Decision:** A pull request push runs ci-light's required checks plus only the suites whose own files it changes. Suites that no merge rule requires leave the every-PR path; the nightly mega tier (`ci.yml`) runs every one of them in full. Merges to `main` gain no new work.
  - `blast-radius.yml` runs only on a pull request labelled `blast-radius`. Locally: `python3 scripts/ci/blast_radius.py --base origin/main`.
  - `e2e-general.yml` runs only on a pull request labelled `e2e-general`, or by dispatch. Never on merges to `main`. Its tests already run nightly in `ci.yml`'s e2e shards (`scripts/ci/e2eshard` partitions every `cmd/e2e` test).
  - `service-binding SDK contract` and `managed storage Node starter` move out of `ci-light.yml` into their own workflows with `pull_request` path filters. `ci.yml` calls both nightly.
  - `function-backends.yml` matches only function-specific files (`*function*` in `pkg/state`, `pkg/sched`, `pkg/fcvm`, `pkg/vmmdgrpc`, `pkg/imaged` and `migrations`, plus its own packages and fixtures). It no longer matches whole shared packages, `go.mod` or the `Makefile`. `ci.yml` calls it nightly.
  - `codeql.yml` runs nightly instead of on every push to `main`.
- **Why:** On 2026-10-29 the hosted pool sat at its 40-concurrent-job cap from 08:30 to 11:15 UTC with about 270 jobs queued across 148 open pull requests. PR jobs waited 13 minutes to start at p50, 31 at p90 and 62 at most. Main's own build fix (#5860) waited more than 50 minutes for a runner. Over 15 hours, pull request events used about 72% of runner minutes, and each push cost about 110–130 job-minutes. The ruleset requires only six ci-light checks (`docs/ci-required-checks.md`); the advisory suites were about two-thirds of PR minutes:

  | Suite | Job-minutes per run | Ran on |
  |---|---|---|
  | e2e-general (3 legs) | 27 | every Go change |
  | function-backends | 22 | 53 of 93 pushes |
  | storage-node-starter (in ci-light) | ~11 | every push and merge |
  | blast-radius | 9 | every push |
  | service-binding contract (in ci-light) | ~3 | every push and merge |
  | codeql on `main` | 28, 31 of 33 cancelled | every merge |

  These cuts take about 35–40% off total runner minutes. They take a typical PR push from about 120 job-minutes to about 60.
- **Consequences:**
  - A regression that only these suites catch shows up the next night instead of before merge, unless the author labels the PR or the change touches the suite's own files. Label `e2e-general` on gateway, routing and daemon-wiring changes. #4153, a gateway that refused every public request, is the class this suite exists for.
  - The blast-radius sticky comment appears only on labelled PRs.
  - CodeQL findings surface up to a day later.
  - Required checks are unchanged. Both new workflows are advisory and keep their job names.
- **Rejected alternatives:**
  - **Run e2e-general on every merge to `main`.** The owner rejected it: merges must not carry extra load, and every merge would compete with PR runs for the same 40 slots.
  - **Make the advisory suites required.** That would put more work on every PR, not less.
  - **Self-hosted runners for PR CI.** The only self-hosted runners are the production control-plane hosts, and heavy work there is ruled out.
