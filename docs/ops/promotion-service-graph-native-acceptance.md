# Frontend/API promotion qualification

The optional `promotion-service-graph` job qualifies destination-bound HTTP
service graphs. Implementation and portable verification are local; actual
native execution and final leakcheck remain pending. Production flags stay off.

Dispatch `e2e-native.yml` on `main` with
`lane=project-environment-promotion-only` and `qualify_service_graph=true`.
The designated acceptance host, source/archive verification, isolated database,
host quiescence and final cleanup requirements from
[environment promotion acceptance](environment-promotion-native-acceptance.md)
apply. This adds a seventh serialized job using the existing
210/215/240-minute test/systemd/job envelopes. The six base jobs and their
eleven-test aggregate remain required and unchanged.

The fixture builds a frontend and API from real source for stable, baseline
and tested generations, plus a separate tenant's caller. Staging uses HTTP
bindings and staging secrets; production requires HTTPS and its own secrets.
An ordinary candidate frontend forwards no release, deployment, revision or
private probe credential. Its network identity must select the complete dark
candidate graph and destination configuration.

Three independent public admissions reuse those artifacts:

1. Regress only the dark candidate API. The frontend's critical probe must fail;
   two real regression rounds refuse promotion without publishing or changing
   the production graph/configuration.
2. Regress the candidate API after healthy preflight and cutover. Two real
   regression rounds must restore both services and the baseline configuration.
3. Recover a healthy candidate API after killing its verified Firecracker
   process. Require no candidate snapshot before this fault, a new guest
   instance, a daemon-owned cold-boot event, unchanged deployment/artifact,
   destination secrets and private graph
   selection. Restart APID, vmmd, gateway and schedd while retaining the durable
   health checkpoint. Complete minute-spaced health windows, then park the
   selected API and wake it through the frontend's binding. This proves graph
   selection across park/wake; it does not assert a particular snapshot tier or
   restore mechanism.

The successful case also attempts forged graph/deployment/revision claims from
the baseline frontend, a conflicting graph claim from the candidate, and a
request from another tenant. Only explicit 403/404/409 denials qualify these
service-proxy checks; outages do not. All cases reject rebuilds, duplicate
candidate allocations, mixed configuration and duplicate publications. Guest
observations contain credential-match booleans, never credential values.
Public-edge forged headers must either be rejected or serve the retained
baseline; they cannot select the private candidate.

Retain `native-e2e.log` from the supplemental partition and the aggregate
`service-graph-qualification.json`. The validator requires the exact two-test
inventory, three attributed cases, deployment/instance identities, configuration
hashes, recovery/isolation evidence, source-bound successful completion and final
clean leakcheck. Synthetic parser fixtures and native-tagged compilation do not
qualify hardware.

For an approval containing `service_graph`, embed the unedited supplemental
report at `capability_reports.service_graph`, alongside the unchanged base
`native_report`. The receipts `service-graph-routing`, `service-graph-recovery`
and `service-graph-isolation` must each use the supplemental report's
`log_sha256`. Review the trusted CI provenance and actual native outcomes before
recording them passed. Sign with both reviewed logs:

```bash
gregalectl promotion-rollout sign \
  --input approval-input.json --sign-key promotion-rollout.key \
  --native-log combined-base-native.log \
  --service-graph-log service-graph-native.log \
  --output approval-signed.json
```

The signer rejects a missing or mismatched supplemental log. Runtime readiness
requires the supplemental report's exact revision/scope/inventory and matching
receipts for `service_graph`. Basic HTTP approvals require the existing base
evidence; the optional report does not change their admission requirements.
Signing and CI report generation do not enable production admission. Follow
[controlled rollout](promotion-rollout.md) for the remaining approval, scope,
expiry, policy and boot-flag requirements.
