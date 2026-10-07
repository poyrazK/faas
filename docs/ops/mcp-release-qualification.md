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

Store receipts in an evidence directory with a manifest:

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

Include all eight rows, using relative artifact paths. Run:

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
