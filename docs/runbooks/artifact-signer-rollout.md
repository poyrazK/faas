# Artifact signer rollout: take sign.key off compute nodes

ADR-20261109170000. Today, every compute node's imaged holds the fleet signing
key, `/etc/faas/secrets/sign.key`. This runbook moves the key to signd on the
control plane and removes it from compute nodes.

Each step is safe on its own and can stop there. Do them in order.

## 1. v2 signatures (after the release with v2 verifiers left the rollback window)

Set `FAAS_ARTIFACT_SIGNATURE_FORMAT=v2` for imaged on the compute nodes. New
layers are now bound to their storage key.

## 2. Turn signd on

1. Control plane: set `faas_signd_listen: "tcp://<control plane private
   address>:8097"` and converge. The control-plane CD then starts signd from
   the active release.
2. Check signd:
   - `systemctl status faas-signd` shows it active;
   - `journalctl -u faas-signd` shows `signd: serving`.
3. Compute nodes: set `faas_imaged_signd_target` to the same address and
   converge, one node first.
4. Deploy an app built on that node. Then check:
   - signd's log shows `signd: signed artifact` for its layer;
   - this query returns a row:
     ```sql
     SELECT storage_key, node_name, signed_at FROM artifact_signatures
     ORDER BY signed_at DESC LIMIT 5;
     ```
   - the app cold-boots.
5. Converge the remaining compute nodes.

From now on imaged signs app layers only through signd, and publishes
runtime bases and the agent layer unsigned (nothing verifies those).

## 3. Re-sign the stored layers

On the control plane, as root:

```sh
gregalectl artifact resign --env-file=/etc/faas/storage.env \
  --db-env=/etc/faas/sealed.env --dry-run
gregalectl artifact resign --env-file=/etc/faas/storage.env \
  --db-env=/etc/faas/sealed.env
```

The pass works on the layer of every deployment that still exists, app or
sidecar:
- It verifies the stored signature against the layer's bytes under the
  signing key.
- A valid v1 signature becomes a v2 one bound to the layer's key.
- App layers get their `artifact_signatures` row (`node_name = 'resign'`).

It never rewrites a signature it cannot verify, and it lists such layers as
`left alone`, exiting 2. Investigate each one: a missing layer, a missing
signature, or one that fails verification.

The pass is idempotent. Rerun it until it reports `converted 0` and no problems.

## 4. Remove sign.key from the compute nodes

Once every compute node runs with `faas_imaged_signd_target` and step 3 is
clean:
- stop staging `COMPUTE_SIGN_KEY` in compute CD;
- delete `/etc/faas/secrets/sign.key` on each compute node.

imaged in signd mode doesn't read the file, so removing it changes nothing
for running builds.

## 5. Refuse v1 (a later release)

After step 3 and one release past the rollback window, verifiers stop
accepting v1 signatures. That is a code change in its own release.

## Rollback

- **Step 2:** clear `faas_imaged_signd_target` on the compute nodes and
  converge. imaged needs `sign.key` back first, so do this before step 4.
- **Step 3:** v2 signatures are read by every release since the v2-verifier
  one, so there is nothing to undo.
