# Gregale start acceptance

Run the local gate from the repository root on Linux with the Go version in
`go.mod`, PostgreSQL, Python 3, `/dev/ptmx`, and CAP_NET_ADMIN in the capability
bounding set (required by schedd). No KVM or platform credentials are needed.

```sh
make e2e-start
```

Set `DATABASE_URL` to a disposable PostgreSQL fixture on which tests can create
isolated schemas/databases. The harness owns migrations and teardown; do not
migrate the shared `public` schema. Unset `FAAS_SKIP_PG_TESTS` and
`FAAS_E2E_BIN_DIR`: the gate refuses skip mode and reused daemon binaries.
The harness builds the current checkout's daemons, and the tests build its CLI.
The command returns nonzero on failure. Use `go test -v -race -count=1
-timeout=20m -run '^TestE2E_Start_' ./cmd/e2e/...` with the same prerequisites for
per-scenario deployment IDs and failure transcripts.
The budget includes cold daemon builds and the complete recovery matrix.

The tests run the CLI as a subprocess with terminal input and isolated config,
cache, and session directories. They drive the actual prompts and assert:

- Public, API-token-protected, and internal apps launch in default and named
  environments.
- An older API, an older gateway, or a gateway outage stops Start before it
  submits any app, secret or source mutation; no deployment is created.
- The source upload reaches apid, and each successful response belongs to the
  accepted app and deployment after a scheduler cold wake. Public edge health
  answers require selecting an app route before verification can succeed.
- An uncommitted file is reviewed, included in the replacement's archive, and
  followed by a response from that replacement. When public responses omit
  deployment identity, the CLI obtains separately labeled owner-check evidence.
  First public launches, including those updating an existing app, require that
  same evidence. Preview identities are validated in every environment.
- A missing required key blocks submission; a selected secrets file is sealed
  into the named environment and excluded from the source archive and session
  metadata. The default scope remains empty.
- A failed replacement retains both its accepted ID and the previous verified
  ID; restarting the CLI can review and retry it.
- Ctrl-C returns 130 after acceptance; resuming checks the saved deployment
  without submitting another one, including interruption of the first launch.
- Dropped commit replies and a killed CLI before receiving acceptance recover
  the exact deployment for first launches and updates in default and named
  environments. Recovery still works after source files and generic upload
  recovery state are removed, with no repeated app, secret or source mutations.
- A failed GET returns nonzero when finished; retrying it checks the same
  deployment without creating another one.
- Session files use mode `0600` and omit credentials, source contents, and
  response bodies. Account credentials do not reach the guest request.
- The live runner's source fixtures, project scan/apply, scoped configuration,
  and cleanup agree with the real API; unrelated apps remain untouched.
- The live runner itself drives failed-build recovery and response retry through
  the real CLI and API in all four public/private, default/named targets. Only
  the local builder/VM boundary is simulated for that contract check.

This gate runs real apid, schedd, both gateways, and PostgreSQL. It simulates
build completion by publishing a signed fixture layer at the state boundary;
the fake VMMD speaks the real streaming protocol and records boot/forward calls.
A disposable trusted TLS adapter forwards preview hosts to the real public edge.
It requires neither external DNS nor changes to the machine's trust store.
The gate does not qualify builder microVMs, guest execution, snapshots, public
DNS/certificates, or native wake latency.

## Nonproduction live gate

Before release, repeat this matrix on an explicitly selected nonproduction
account/project with its normal credentials and dedicated native x86_64 Linux
KVM capacity. Confirm the account, API, project, and environment in the review
screen. Use disposable apps and fixture-only values; do not use customer data.

### Repeatable core lifecycle runner

`make native-start-acceptance` builds the current checkout's CLI and drives its
real terminal prompts against an already-installed nonproduction fleet. It
creates a fresh project with four disposable Node apps through the normal
project scan/apply API. Builds run on that fleet's builders; no completion or
guest response is synthesized. Each public/private, default/named target covers:

- Start launch, Ctrl-C after the accepted ID is saved (exit 130), and restart
  without a duplicate deployment.
- Source edit, review, redeploy, and a nonce returned from the updated guest.
- Anonymous public TLS access or private denial, plus independently checked
  accepted/served app, deployment, environment, and instance identities.
- Idle park (`PARKED` and `resident=false` in scheduler inventory), then Start
  resume and a new wake ID.
  The selected instance's timeline must contain scheduler boot/completion and
  VM boot/readiness events identifying that wake. Cold boot and snapshot restore
  are recorded separately; this runner does not certify the p95 latency target.
- A reviewed source update whose Node build script exits 42. The accepted
  deployment and its exact build must report a user build failure. Start must
  retain the failed ID and previous verified ID after finishing with exit 1 and
  restarting. Fixing the source requires another review and creates exactly one
  replacement; the old IDs remain inspectable.
- A guest HTTP 503 on the verified deployment. The fixture has per-instance
  arm/recover GET routes containing its run nonce; only the root response fails.
  Start must show the failed check and exit 1 when finished. Restarting and
  selecting Retry after restoring HTTP 200 verifies the same deployment and
  instance, with no new deployment. These controls belong only
  to the disposable fixture; no fleet services or gateway settings are changed.

The runner follows the existing native acceptance host-lock convention. It
requires accessible `/dev/kvm`, a bare-metal x86_64 Linux host, and a root-owned
regular `/etc/faas/start-acceptance-host.json` without group/other write access.
The operator writes this designation only after checking the dedicated fleet
and its installed daemon/guest release. Its contents bind the runner to the
nonproduction API and account:

```json
{
  "native_x86": true,
  "api_origin": "https://api.acceptance.example.com",
  "account_id": "11111111-1111-4111-8111-111111111111",
  "platform_sha": "0123456789abcdef0123456789abcdef01234567"
}
```

Supply `FAAS_TOKEN` through the normal secure environment, then select the
matching Pro/Scale fixture account, an unused `start-` project slug of at most
20 characters, and a named environment. Existing projects and app slugs are
rejected before apply. This command incurs normal nonproduction build/runtime
usage and removes the resources it creates.

```sh
export FAAS_API=https://api.acceptance.example.com
export FAAS_START_ACCEPTANCE_ACCOUNT=11111111-1111-4111-8111-111111111111
export FAAS_START_ACCEPTANCE_PROJECT=start-unique-run
export FAAS_START_ACCEPTANCE_ENVIRONMENT=staging
make native-start-acceptance
```

`FAAS_START_EVIDENCE_ROOT` optionally selects the evidence parent directory;
each invocation creates a private `run-*` directory. `acceptance.json` contains
the checkout SHA, dirty-source fingerprint, CLI and fault-relay binary hashes, installed fleet
SHA, target/resource IDs, per-scenario results, current milestone, and cleanup
status. The saved fields are an allowlist: raw terminal transcripts, response
bodies, credentials, and local secret-file paths are never written to evidence.
Terminal output and API reads are bounded. Keep failed evidence for inspection.
Each case includes `recovery.failed_build`, `recovery.response_retry`, and
`recovery.missing_configuration` evidence, plus `recovery.compatibility` and
`recovery.lost_commit_reply`
with saved accepted/verified IDs, exact failed build identity/class, repaired
source digests, failed/recovered HTTP status, and whether deployment history
stayed unchanged during inspection and GET retries. The runner records these
identities as it progresses, so a failed run still explains where it stopped.

The configuration case edits a verified fixture to require a fresh random
fixture-only secret. It checks that the blocker and subsequent key review leave
deployment history and both scopes unchanged until confirmation. Start reads a
private KEY=VALUE file inside the source directory, seals the key into the
selected scope, and excludes that file from source packing. The fixture's build
script rejects the selected filename or a leaked secret assignment and writes a
generated source-check stamp. The guest must report both that stamp and a match
against the expected secret digest; it returns only booleans, never the value.
The runner verifies that the other scope remains empty, removes the local file,
and resumes the same deployment without another submission. It scans final CLI
output, local recovery/config/cache JSON, and evidence for the fixture value and
file path. The saved result contains only IDs, key/scope names, source digests,
response identity/status, and assertion outcomes.

Compatibility coverage creates one intentionally reviewed baseline on the same
owned app through a separate, authenticated loopback API relay. Its recovery
record stays bound to that one relay address for the whole case. The core CLI
scenarios still connect directly to the designated API, and their saved session
files must remain byte-for-byte unchanged. The relay forwards to the designated
API using the inherited proxy and TLS trust settings; only the fixture CLI uses
its loopback HTTP address. Public ingress continues through its normal HTTPS
route. No fleet daemon versions, routing settings, or credentials are changed.

For each public/private and default/named target, the relay injects a missing API
capability endpoint, an unsupported gateway capability, and an unavailable
verification service. Start must reject a fresh launch and a redeploy whose
preflight becomes unavailable after source/secrets review. A mutation guard
rejects writes during these phases; an attempted write fails qualification even
if the guard prevented it from reaching the API. Evidence includes counters for
app creation, secret changes, source submission, other writes, capability
failures, and owner verification POSTs. The last category is a read-only GET
against the guest, not a deployment mutation. The healthy baseline cannot create
an unpinned app either.

The runner checks upgrade versus temporary-outage guidance, saved accepted and
verified IDs, unchanged configuration versions, and recovery without another
deployment. Temporary failures exit unsuccessfully, survive restart, and then
recover using the offered retry. Private cases also fail the owner response-check
POST after a healthy preflight. A public app may still verify successfully if
its HTTPS response independently proves the exact deployment; the runner keeps
that valid success. Relay evidence contains only fixed categories and counters.
No credentials, request bodies, uploads, or raw transcripts are recorded.

These scenarios qualify the CLI's behavior under injected wire failures against
the selected fleet. They do not install older binaries or certify a mixed-release
rollout; use the release readiness gates for actual version convergence.

Lost-acceptance coverage runs before each core lifecycle case, using separate
source directories and recovery records through a stable relay connection. For
both a first launch and an update, the API commits normally. The relay captures
only the exact deployment, app, and upload IDs, then either drops every commit
reply (including retries) or holds the reply while the runner kills its own CLI
process. No acceptance headers or body reach the CLI. Lost replies exit 3; the
held process exits through SIGKILL. The runner checks that the durable pending
upload matches server-side committed state and retains the previous accepted
and verified IDs (empty for a first launch).

The runner then deletes the generated source files, upload state/cache, and
local cache directory. Only the project link remains in the source directory so
the recovery context stays the same. With a mutation guard active, a restarted
CLI must discover that upload, recover and verify the exact committed deployment,
clear the pending record, and finish successfully. Any attempted app, secret,
source, or other write fails qualification even if blocked. Both scopes' configuration
versions and the complete deployment inventory must remain unchanged during
recovery. Evidence stores only protocol IDs, source digests, exit statuses,
bounded request counters, and assertion outcomes. The original direct-API
session files stay unchanged; the runner never rewrites a session's API binding.

Cleanup runs on failure or interruption with its own deadline. It deletes only
app IDs pinned in the apply receipt, verifies their current identities, removes
the same project after its workloads are gone, and waits for owned instances to
leave the live inventory. Host resource leak checks remain part of the native
platform gates. A changed identity, new project member, unknown apply
outcome, or API outage leaves cleanup failed and visible. It never guesses IDs
from the newest deployment or deletes by a slug prefix. To retry pinned cleanup,
with the same selected API/account/project/environment and host lock:

```sh
flock -w 30 /var/lock/faas-builder-acceptance.lock \
  python3 scripts/ci/start-native.py --out /path/to/run-directory --cleanup
```

Cleanup still verifies the host designation and account identity, but does not
require working KVM or gateway response checks; an outage there must not prevent
removing pinned fixtures through a healthy API.

`make test-start-native-acceptance` runs portable runner checks and verifies the
fixture sources through the real repository scanner. When Node is available it
also executes the failing build script and the fixture's HTTP fault/recovery
routes, configuration checks, and source guard locally. `make e2e-start` checks
provisioning/cleanup against the local API and drives the runner's recovery
methods through the real CLI and local daemons. Its configuration cases inspect
the actual accepted source archive for the secret file, value, and path, and
derive the simulated guest result by decrypting the scoped ciphertext sent in
that instance's VM RPC. Builder execution and native guest delivery remain
simulated in this local gate.
Portable checks also exercise the relay's streaming, authentication, mutation
guard, TLS verification, fault replies, and audit counters. The local CLI/API
matrix drives all compatibility modes and the sixteen lost-reply/interruption
probes through this same relay executable. Submission cases use fresh fixture
apps per fault to keep the real concurrency limit while testing first launches
and updates.
Neither command qualifies a native fleet. A passing native runner covers the core
lifecycle, configuration blockers, failed-build recovery, response retries, and
injected compatibility/outage and lost-acceptance cases below. Its
`remaining_live_matrix` is empty once all automated scenarios are implemented;
only a completed native run supplies live qualification evidence.

| Scenario | Required evidence |
| --- | --- |
| Public and private apps, default and named environment | Accepted ID, successful status, correct served deployment; private access remains enforced |
| Edit, review, deploy again | Changed source reaches the guest; new accepted ID matches the served ID |
| Park, then check again | Scheduler/VM evidence identifies the selected deployment and native cold boot or snapshot restore |
| Missing required configuration | No submission or configuration mutation before confirmation; exact fixture value reaches only the selected scope; secret file/assignment excluded from build source; resume without the local file uses the same ID |
| Failed build | Failure is visible; accepted and previously verified IDs remain inspectable; fixed source can be reviewed and deployed |
| Ctrl-C after acceptance, restart, resume | Exit 130, same accepted ID, no duplicate deployment, successful response after recovery |
| Response failure and retry | Non-2xx is not reported as verified; retry checks the same accepted ID |
| Injected incompatible API/gateway capability or verification-service outage | Fresh and confirmed launches submit no mutations; upgrade/outage guidance is distinct from an app error; saved IDs survive restart and recovery creates no duplicate deployment |
| Commit reply lost or CLI killed before receiving acceptance, first launch and update | Pending upload identifies the exact committed ID and preserves previous accepted/verified IDs; restart without source files or upload cache discovers and verifies the same ID, attempts no writes, and changes no deployment/configuration inventory |

Save the checkout revision, CLI/server release identities, target IDs, scenario
results, and bounded redacted logs with the release evidence. Keep tokens,
secret-file paths/contents, response bodies containing customer data, and raw
terminal recordings out of published evidence. Remove only the disposable
resources created for this run, using their recorded identities.

## Review and rollout

Review the CLI and response-check contract changes together with
[ADR-1658](../adr/1658-owner-authorized-deployment-response-checks.md). Attach
local gate output and live results to the review; incomplete live evidence stays
explicitly pending. Follow the
[release readiness checklist](release-readiness-checklist.md) for signed bundles
and fleet convergence.

Deploy the gateway response-check capability before enabling its apid route,
then release the CLI. Smoke-check a private named-environment deployment before
expanding the rollout. An older server must return an unsupported check failure;
the CLI must not fall back to an unscoped invocation. If reverting, withdraw the
CLI first, then the apid route, then the gateway capability; retain accepted
deployment records so existing sessions remain inspectable.
The gateway capability endpoint and API compatibility reader must ship with their
response-check handlers. Verify both advertise protocol 1 without allocating
capacity. Start repeats compatibility checks at the mutation boundary; a positive
preflight remains a point-in-time observation rather than a health guarantee.
