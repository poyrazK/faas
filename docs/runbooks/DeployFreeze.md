# Deploy freeze and the deploy preflight

Decision: [ADR 20261020214632](../adr/20261020214632-automatic-unit-deploy-safety-rails.md) §2.

Automatic per-unit deploys (`release-units.yml`, when the repository variable
`UNIT_RELEASE_AUTODEPLOY_ENVIRONMENT` is set) start only after the
**Check production can take a deploy** job finds production unfrozen, idle
and healthy. Its step summary lists every check, what it waited for and for
how long.

## Freeze automatic deploys

Open an issue with the `deploy-freeze` label and say why in the title:

```bash
gh issue create --repo poyrazK/faas --label deploy-freeze \
  --title "Deploy freeze: <reason>" --body "<who, until when, what to watch>"
```

Only people with triage access or more can apply labels. The first time,
create the label: `gh label create deploy-freeze --repo poyrazK/faas
--description "Holds automatic per-unit deploys"`.

While any open issue has the label, a unit release run ends green with
**Held, not deployed** and changes nothing. Merges keep building unit
releases. They accumulate over the base full release, so nothing is lost.

## Lift it

Close the issue. Every open `deploy-freeze` issue holds deploys, so close
all of them. The next merge deploys everything merged since the last deploy.
If nothing merges, start that deploy yourself:

```bash
gh workflow run release-units.yml --repo poyrazK/faas -f deploy_environment=production-us
```

## Deploy during a freeze (repair)

Dispatch the lanes directly. That path skips the preflight, and the run
records who started it:

```bash
gh workflow run cd-controlplane.yml --repo poyrazK/faas \
  -f deploy_environment=production-us -f units_run_id=<release-units run id>
```

Rollbacks are never frozen.

## Why did my merge not deploy?

Open the release-units run for the merge commit and read the **Deploy
preflight** summary:

| outcome | meaning | what happens next |
| --- | --- | --- |
| Held, not deployed | a `deploy-freeze` issue is open | lift the freeze (above) |
| Stood down | a newer merge's unit release deploys instead | nothing: unit releases are cumulative |
| Not deployed: main's light tier failed | the commit's post-merge light run is red | fix main; the next green merge deploys |
| Not deployed: waited N s | another deploy, the deploy lock or a page alert held it for 40 min | the next merge tries again, or dispatch release-units once the cause clears |

The checks:

- **freeze:** no open `deploy-freeze` issue.
- **newest:** no newer release-units push run is about to deploy.
- **main:** the commit's post-merge ci-light run has not failed.
- **idle:**
  - no run of cd-platform, cd-compute, cd-controlplane, cd-edge-units or
    cd-edge-node is queued or running;
  - no other release-units run is in a deploy job;
  - the control plane's deploy lock (`/run/lock/faas-deploy.lock`) is free.
- **healthy:** the control plane's Prometheus answers, and no page-severity
  alert fires there. Alerts in the families `traffic_anomaly`,
  `tenant_abuse`, `security_anomaly` and `residency` are ignored, as in the
  SLO gate. A silenced alert still fires in Prometheus and still holds
  deploys. To deploy anyway, use the repair path.
