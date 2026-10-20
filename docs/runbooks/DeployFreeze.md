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
| Not deployed: waited N s | another deploy, the deploy lock, a page alert or a full watch held it for 40 min | the next merge tries again, or dispatch release-units once the cause clears |
| Not deployed: ... adopt=true | a host holds two unwatched releases and no watch runs for them | adopt the record (below) |

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
- **watch:** no host holds two unit releases that have not finished their
  post-deploy watch (`release_units.json` `watch.max_unwatched`). While a
  watch runs, the preflight waits for it. If none runs, it fails and asks
  for an adoption (below).

## The post-deploy watch

After its lanes, every unit release run keeps the SLO gate running for
30 minutes after its last activation (the **Watch the deployed units** job,
`cd-unit-watch.yml`). The control plane's watch record
(`/var/lib/faas/deploy/unit-watch.json`) lists, for each host, the releases
deployed since its last clean watch and the **known-good** release before
them. A newer run that deploys meanwhile takes the record over and watches
both releases. The older watch then ends green with **Superseded**.

| outcome | what happened | what to do |
| --- | --- | --- |
| Passed | no SLO breached and no post-merge light run failed | nothing: the hosts' releases are now known-good |
| Superseded | a newer run, or a person or full release deploying, took over | nothing |
| Breach, automatic rollback | every recorded host went back to its known-good release, and a `deploy-freeze` issue holds the report | read the issue, then revert or fix the cause and close it |
| Inconclusive | Prometheus gave no evidence for 5 minutes | check Prometheus. The record stays, and the next watch covers it |

A unit deploy lane that fails (an activation or SLO gate rolled its host
back, or the lane failed before changing anything) also opens a
`deploy-freeze` issue. The next merge's unit release would contain the same
change.

### Adopt a record no watch runs for

When a watch ended inconclusive or was cancelled, and a host already holds
the most unwatched releases allowed, the preflight fails with
`dispatch cd-unit-watch.yml with adopt=true`. Check production first, then:

```bash
gh workflow run cd-unit-watch.yml --repo poyrazK/faas \
  -f deploy_environment=production-us -f units_run_id=<the newest release-units run id> -f adopt=true
```

The adopting run owns the record and watches a full 30 minutes from now.
On a breach it rolls back to the known-good releases with that run's signed
deployctl.

### Read the record

```bash
ssh root@<control plane> cat /var/lib/faas/deploy/unit-watch.json
```

Do not edit it by hand. If it is deleted, the next watch can roll back only
to the release its own deploy replaced.
