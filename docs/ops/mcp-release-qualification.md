# MCP release qualification

MCP hosting remains preview until the reviewed build has evidence for every
check below. Portable tests qualify protocol handling and PostgreSQL correctness;
they do not qualify Firecracker restore or provider/client login interoperability.
Use a dedicated test account and native x86_64 host with KVM. Keep credentials
in environment bindings and exclude tokens, subjects and customer payloads from
stored evidence.

| Evidence name | Required observation |
| --- | --- |
| `untouched_lockfile_deploy` | Deploy the scaffold with every original npm integrity field; record deployment ID, source digest, lockfile digest and CLI promotion receipt. |
| `native_cold_boot` | Verify a candidate from a cold boot with no reusable snapshot; record its deployment, host/build versions and wake classification. |
| `native_park_restore` | Park, verify discovery and harmless live progress after restore, then park again and record no remaining active instance. |
| `oauth_chatgpt_login` | Connect in ChatGPT through the selected real OAuth provider: discovery, PKCE, resource audience, consent, valid request and rejection after expiry. |
| `oauth_codex_login` | Repeat the real provider flow in Codex and record provider/client versions. |
| `official_sdk_interop` | Run modern and legacy discovery and harmless live progress using the pinned official SDK client. |
| `tasks_restart_replicas` | Create a task, kill/restart its worker, resume with two replicas, fence the stale lease, and retrieve the result only as its owner. Include final-attempt input resume. |
| `tasks_scale_from_zero` | Keep a separate observer running, start with zero worker replicas, admit work, observe worker creation and completion, then return to zero. |

Reject a candidate with bad protocol behavior or a widened reader catalog and
record the previous serving revision still at 100%. Repeat promotion with a
concurrent serving revision change and record the 409 without a traffic write.
Use explicit harmless probe tools; deployment/catalog gates never execute tools.
Schema migrations, sealed binding changes and gateway policy updates are
separate app-wide operations and need their own reviewed rollout.

Create a private workspace for the reviewed commit with the qualification runner:

```sh
python3 scripts/ops/mcp-qualification.py init --dir <evidence> --commit <reviewed-sha>
```

This writes a manifest with all eight rows pending. After each real observation,
save a redacted receipt under that directory and record its exact hash:

```sh
python3 scripts/ops/mcp-qualification.py record --dir <evidence> \
  --name untouched_lockfile_deploy --target '<test app and deployment>' \
  --artifact untouched-lockfile-deploy.json --status passed
python3 scripts/ops/mcp-qualification.py status --dir <evidence>
```

Repeat `record` for each required row. Use `--status failed` to preserve a failed
observation; the gate remains closed. The runner records operator-supplied
receipts and does not deploy apps or contact OAuth providers, clients or hosts.
Do not commit credentials or unredacted customer data.

The manifest has this shape:

```json
{
  "version": 1,
  "commit": "<full reviewed 40-character commit SHA>",
  "checks": [
    {
      "name": "untouched_lockfile_deploy",
      "status": "passed",
      "target": "<test app, native host and build identifiers>",
      "artifact": "untouched-lockfile-deploy.json",
      "sha256": "<SHA-256 of the exact redacted artifact>"
    }
  ]
}
```

Include all eight rows, using relative artifact paths. Run the local release
check:

```sh
python3 scripts/ops/mcp-qualification.py check --dir <evidence> --commit <reviewed-sha>
```

The underlying checker is also callable directly:

```sh
python3 scripts/ci/mcp-qualification-check.py <evidence>/manifest.json --commit <reviewed-sha>
```

The MCP contract workflow also accepts `qualification-manifest` and
`reviewed-commit` dispatch inputs to run this gate against supplied evidence.

The gate fails for missing, skipped or failed checks, mismatched source commits,
missing receipts, path escapes and changed artifact digests. It checks evidence
completeness and integrity; a reviewer must assess whether each recorded
observation proves the corresponding native or provider/client behavior. The
2026-10-01 demo evidence remains historical and does not pass this release gate.

### Disposable native Task rollout harness

`scripts/ops/mcp-hosting-rollout-qualification.py` exercises the native release adapter against a dedicated test account. Local contract tests do **not** constitute live qualification. Its reports cover Task durability and release safety; they do not replace the OAuth, official SDK, cold-boot, or park/restore evidence required above. Tasks are seeded through the database store, rather than an authenticated MCP client.

Build the Gregale binary from the reviewed commit. Prepare disposable fixtures:

```sh
python3 scripts/ops/mcp-hosting-rollout-qualification.py prepare \
  --starter cmd/gregale/templates/mcp-node --dir /tmp/mcp-qualification-fixtures
```

Install each fixture's locked npm dependencies before running. Provision separate `mcp-qual-` web, previous-worker, candidate-worker, and observer apps in a dedicated qualification account. The candidate app must have no instances or deployments; worker and observer scaling must have minimum one. Configure remote `DATABASE_URL` secrets with the runtime account for web/workers and the observer account for the observer. All apps share the disposable namespace and owner key. Follow the migration and role-grant instructions above to establish four distinct database accounts in the same schema; grant them their runtime, observer, operator, and migration profiles. The three service accounts must not have schema CREATE.

Supply the runner's environment bindings: `MCP_QUAL_RUNTIME_DATABASE_URL`, `MCP_QUAL_OBSERVER_DATABASE_URL`, `MCP_QUAL_OPERATOR_DATABASE_URL`, `MCP_TASK_MIGRATION_DATABASE_URL`, `MCP_TASK_OWNER_KEY`, and `MCP_TASK_NAMESPACE` (starting with `mcp-qual-`). Configure CLI authentication separately. Do not put secrets in plans or evidence.

Create a native release plan with paths to the prepared `web` and `worker-candidate` directories, one previous worker app, and the observer plus its metric destination. Use `web-bad` for the unhealthy-candidate scenario. Then run:

```sh
python3 scripts/ops/mcp-hosting-rollout-qualification.py run \
  --plan /tmp/qualification-plan.json --state /tmp/qualification-state.json \
  --fixtures /tmp/mcp-qualification-fixtures --binary /tmp/gregale \
  --account DEDICATED_ACCOUNT_ID --commit FULL_REVIEWED_COMMIT_SHA \
  --scenario rollout --bootstrap --evidence /tmp/qualification-rollout.json
```

Run all four scenarios (`rollout`, `interrupt`, `bad-candidate`, `stale-observer`) with fresh app sets, namespaces, journals, and evidence files. `--bootstrap` deploys the old worker, observer, and initial web fixture; omit it only when those fixtures are already deployed. The interrupt scenario terminates the release after its durable web submission checkpoint, resumes the same journal, and compares deployment IDs. If that window cannot be observed, the run fails. Successful rollouts require all three probe Tasks to complete on the candidate's runtime database account. Failure scenarios require rejection at the readiness gate, unchanged web traffic, and a running previous worker.

Before account access, the runner verifies that the local source checkout is clean at the requested commit and that `go version -m` reports the same revision with `vcs.modified=false` for the supplied binary. It records those provenance fields and the binary and plan digests in each report. Review the report's native observations, database accounts, initial Task states, and completion/preservation evidence. Reports contain fixed probe metadata and IDs, never database URLs or Task payloads. Missing bindings produce a **blocked** report. Go build metadata provides a consistency check, not a signed attestation.

The fixture handlers deliberately keep work running or retrying. The stale-observer scenario parks its observer. Cleanup is manual: retain evidence and journals, then cancel remaining probe Tasks and remove the disposable apps, namespace data, and database accounts. Never deploy these fixtures into customer applications.
