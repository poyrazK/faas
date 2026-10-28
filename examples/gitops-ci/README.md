# GitOps plans in pull and merge requests

Use a runner with a pinned `gregale` CLI containing `gitops plan`. Before a
release, build it from this reviewed checkout with Go 1.26.10:

```sh
go build -o bin/gregale ./cmd/gregale
bin/gregale environment gitops plan shop staging \
  --commit "$CANDIDATE_SHA" --format markdown --fail-on-blocked
```

No KVM host is needed. Set `FAAS_API`, store a `project_environments:read` key
as `FAAS_TOKEN`, and set `GREGALE_PROJECT` and `GREGALE_ENVIRONMENT`. Bind that
environment to the pipeline's repository and definition. Gregale fetches the
exact commit through its verified provider connection. The job does not upload
or execute the app checkout.

- [GitHub Actions](github-actions.yaml) writes a job summary.
- [GitLab CI](gitlab-ci.yaml) retains a merge-request job artifact.

The examples use the `gregale-cli` runner label/tag; adapt them to your existing
CLI installation. Both CLI and API must include the new endpoint. Store tokens
in trusted CI secrets. Fork pipelines without these credentials require a
separately authorized trusted pipeline. No provider write permission or GitLab
OAuth scope change is needed.

Exit 0 means this observation has no plan blockers. Exit 2 means a blocked plan
was produced. Exit 1 means the preview could not be obtained or validated.
JSON is the default and can be retained as an artifact:

```sh
gregale environment gitops plan shop staging \
  --commit "$CANDIDATE_SHA" --fail-on-blocked > gitops-plan.json
```

Values are redacted. The result identifies its commit, source generation and
observed intent version. It changes no environment state and does not approve,
adopt, deploy or prove readiness. Rerun after source, ownership, override or
environment changes. Approval and adoption retain their separate receipts; a
candidate plan cannot authorize either action.

## Wait after merge

The optional [GitHub Actions rollout example](github-actions-rollout.yaml) and
[GitLab rollout example](gitlab-ci-rollout.yaml) wait for the exact default-branch
push commit after merge, using the same read-only API scope. Pin a CLI and API
version supporting gitops wait and current evidence commit_sha. Bind the selected
environment to that repository/ref and manifest. Run this only where normal
approval and qualified controllers are already operating; report mode fails
explicitly and the command never enables execution.

The GitHub example retains the final JSON in the job summary; GitLab retains it
as an artifact even on failure. Neither job checks out or runs the application.
Use protected CI variables/secrets and a runner with the pinned CLI installed.
Adjust the default branch and timeout for the environment. Scheduled workloads
may need a deadline long enough to observe a post-activation occurrence.

Exit 0 proves current serving for the requested commit. Exit 2 is failed/blocked,
3 is timeout, 4 is superseded, 130 is interruption, and 1 is a request/output
error. A historical healthy release cannot pass this job. See the
[wait semantics](../../docs/environment-gitops.md#wait-for-a-merged-commit-to-serve).
Native VM/leak acceptance and production gates remain separate requirements.
