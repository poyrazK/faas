# FaasPlatformFlagPastRetireBy

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.

`FaasPlatformFlagPastRetireBy` fires when a preview platform flag has passed
the retire-by date in its catalog entry (`pkg/platformflags/catalog.go`,
ADR 20261021080640). It is a ticket, not an outage: the flag keeps deciding
exactly as before. The alert exists so preview gates are folded into general
availability or removed instead of becoming permanent.

## Symptoms

A `warn` ticket names the flag in its `flag` label. apid exports the date as
`apid_platform_flag_retire_by_timestamp_seconds{flag}`, and
`gregalectl flags list` marks the flag as overdue.

## Check

1. See who the flag is released to and whether anything still depends on it
   being off:

   ```bash
   gregalectl flags show --key <flag>
   gregalectl flags list
   ```

2. Confirm the date from the metric and the catalog entry:

   ```promql
   apid_platform_flag_retire_by_timestamp_seconds{flag="<flag>"}
   ```

3. Ask the owning team (the ADR that `flags show` prints) whether the preview is
   ready for everyone.

## Recover

- **Ready:** release it to everyone
  (`gregalectl flags default --key <flag> --value true --reason ... --yes`),
  let it run for a release, then remove the gate and the catalog entry in a PR. The alert clears when the deployed apid no longer
  exports the flag.
- **Not ready:** move `RetireBy` in `pkg/platformflags/catalog.go` to a new
  date and give the reason in the PR description. The alert clears once the
  new apid is deployed.

Do not silence the alert indefinitely; an undated preview gate is what this
alert prevents.
