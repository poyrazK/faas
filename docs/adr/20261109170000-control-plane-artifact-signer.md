# ADR-20261109170000: Build artifacts are signed on the control plane, bound to their storage key

- Status: Accepted (design; implementation lands in slices)
- Date: 2026-11-09
- Amends: ADR-038 (build attestation), ADR-125 (fleet signing key)
- Related: ADR 20261022111812 (seal domains: no fleet-wide secrets on
  compute nodes), ADR-20261023104734 (per-daemon database roles),
  `docs/runbooks/compute-node-compromise.md`

## Context

ADR-038 signs every app layer imaged builds. imaged on each compute node
holds the private key, `/etc/faas/secrets/sign.key`, which is one P-256 key
for the whole fleet. It signs SHA-256 of the layer's bytes and writes the raw
64-byte signature to `sigs/<storage key>.sig`. schedd verifies it with
`sign-pub.pem` before a cold boot, and apid before a rollback.

Compute nodes run untrusted guest code. Root on any one of them, for example
after a guest escape, gets:

1. **The fleet signing key.** The holder can sign any bytes, so any layer it
   writes verifies as a build.
2. **Write access to the artifacts store.** Compute daemons put and delete
   layers.
3. **Unbound signatures.** A signature covers the bytes only, not which
   artifact they belong to. Even without the key, the holder can copy app A's
   signed layer and signature over app B's keys, and B's next cold boot runs
   A's code with B's secrets.

Together these allow persistent cross-tenant code injection that survives
re-imaging the compromised node.

## Decision

### 1. A dedicated signer, `signd`, on the control plane holds the key

`signd` is a small daemon that runs only on control-plane hosts. It is the
only holder of `sign.key`. It listens on the control plane's private address
over mTLS and serves one RPC, `SignArtifact`. Its database role,
`faas_signd`, may read deployments, apps and image preparations and write
only its own `artifact_signatures` table. No other role may write that
table.

`sign.key` is no longer staged on compute nodes. Compute nodes keep
`sign-pub.pem`.

### 2. Signatures are bound to the artifact

A version-2 signature covers this payload:

```
gregale.artifact-signature.v2
<storage key>
<sha256 hex of the artifact>
```

ECDSA P-256 is applied over SHA-256 of the payload. `sigs/<key>.sig` holds a
small JSON document: the format, the key, the digest and the signature. An
app layer's key already names its deployment (`apps/<slug>/<deployment
id>.ext4`); signd records the deployment beside the signature (section 3).

A verifier recomputes the artifact's digest and checks every field against
the key it is about to boot. A layer copied over another app's key
then fails, because the signed key differs. Version-1 signatures (raw 64
bytes) stay accepted until the re-sign pass (section 5) has converted every
stored layer and a release has passed. After that, verifiers refuse them.

### 3. Compute asks; the control plane decides

imaged keeps building layers and computing their digest as it does today
(`rootfs.ArtifactIdentity`). In place of `LocalSigner.Sign` it calls
`SignArtifact(deployment id, storage key, digest, bytes)` with its node's
imaged mTLS leaf. signd signs only when all of the following hold:

- the client certificate is an imaged leaf of a registered compute node, and
  its node name is the image preparation's `node_name`;
- the deployment is mid-build: its image preparation is in phase
  `preparing`, and the deployment has not reached a published state;
- the storage key is one the deployment's build may produce. signd derives
  the key from the app slug and deployment id (`storagekey.AppLayer`), and
  likewise for sidecar and agent-base layers; it never takes the key as
  given;
- **no signature exists yet for that key.** signd inserts into
  `artifact_signatures(storage_key primary key, digest, deployment_id,
  node_name, signed_at)` in the same transaction it signs in, and refuses
  any second signature for a key.

signd writes the `.sig` object itself, so compute needs no write access to
`sigs/`.

The image preparation row is written by compute, so a compromised node could
forge a claim. That is acceptable because of the last two rules. A forged
claim can only target a deployment that is building right now and has no
signed layer. That is the same thing a malicious builder node could do to a
build placed on it. It can never re-sign an existing layer, and with bound
signatures it can't swap one either.

### 4. The digest comes from compute

signd does not re-read layers, which can be 1–2 GB. A compromised node can
have signd sign bad content for a build it is legitimately running, which it
could also produce by building it. Re-hashing on the control plane would not
prevent that, and would put every build's bytes through the control plane.

### 5. Rollout

1. **Release N:** verifiers accept both v1 and v2 signatures. imaged can
   write v2 with its local key (`FAAS_ARTIFACT_SIGNATURE_FORMAT=v2`), which
   already binds new layers to their key; it is turned on once release N-1,
   whose verifiers read v1 only, has left the rollback window. signd ships,
   disabled unless configured.
2. **Turn signd on:** control-plane converge installs signd with `sign.key`.
   imaged switches to `SignArtifact` with `FAAS_ARTIFACT_SIGNER=signd`; while
   unset, imaged signs locally as today.
3. **Re-sign pass:** `gregalectl artifacts resign` runs once on the control
   plane with signd's key. It reads each stored layer, verifies its v1
   signature, and writes the v2 signature and the `artifact_signatures` row.
   A layer whose v1 signature does not verify is reported and left alone.
4. **Stop staging `sign.key` on compute.** Node join removes it. CD keeps it
   only in the control-plane secret set.
5. **Release N+1, after the rollback window:** verifiers refuse v1.

### 6. Rotation

signd signs with one active key and publishes `sign-pub.pem` plus the
previous public key during an overlap. Verifiers accept both. Rotation no
longer touches compute nodes beyond the public key.

## Implementation

- **Slice 1** (v2 format, verifiers accept v1 and v2,
  `FAAS_ARTIFACT_SIGNATURE_FORMAT`): `pkg/cosign`.
- **Slice 2** (signd, off until configured):
  - `api/proto/onebox/faas/signd/v1` defines `SignArtifact`.
  - `pkg/signd` holds the policy (section 3), the client imaged uses, and
    `signpg`, its own sqlc store.
  - `cmd/signd` is the daemon. It listens on TCP with mutual TLS only, and
    its handshake admits only registered compute nodes (`PGNodeVerifier`).
  - Migration `20261109220714634` creates `artifact_signatures`. Only
    `faas_signd` may insert, and `runtime-roles.sql` repeats the grant on
    every converge. signd's OS user `faas-signd` maps to `faas_signd` and
    never to the owner.
  - The leaves are the pkg/pki feature `artifact-signer`, issued by
    `gregalectl pki feature`: signd's server leaf (`signd.faas`) and
    imaged's client leaf under `signd-client/`, whose CN is the node's name.
  - imaged sends app and sidecar layers to signd when
    `FAAS_ARTIFACT_SIGNER=signd` and `FAAS_SIGND_TARGET` / `FAAS_SIGND_TLS_*`
    are set. Runtime bases and the agent layer, which no verifier checks
    today, keep the node's local signer until slice 3 decides them.
- **Slice 2b** (deploy, off until set):
  - The `signd_service` role, run on every control-plane converge, installs
    the `faas-signd` user, the unit (`UnitSignd`, release lane
    `control-plane`), signd's server leaf, and a drop-in. That drop-in hands
    `sign.key` and the TLS key to signd as credentials and logs it in as
    `faas_signd`.
  - It turns on with `faas_signd_listen` (`tcp://<private address>:8097`).
    CD starts signd from the active release and waits for its listener.
  - The host firewall opens 8097 to the compute CIDRs.
  - On compute nodes, `faas_imaged_signd_target` issues imaged's client leaf
    on the control plane and installs imaged's signd drop-in.
  - `pki_renew.yml` renews both leaves.
- **Next (slice 3):**
  - `gregalectl artifacts resign`, an operator CLI run once on the control
    plane, idempotent, with a dry run;
  - imaged stops signing runtime bases and the agent layer, which no verifier
    checks (user decision, 2026-11-10), so a node without `sign.key` builds
    them unsigned;
  - node join stops staging `sign.key` on compute;
  - verifiers refuse v1 at N+1.

## Consequences

- Root on a compute node can no longer sign arbitrary layers, re-sign stored
  ones, or move a signed layer to another app. Its reach is limited to builds
  it is running or races for.
- Builds depend on the control plane being reachable to finish. Wakes don't:
  verification stays local, with `sign-pub.pem`.
- One more daemon on the control plane: its unit, role, database role and
  release entry. It holds a single key and serves a single RPC.
- `artifact_signatures` also gives an audit trail: which node built which
  artifact, and when.

## Rejected alternatives

- **Per-node signing keys with a trust list.** A compromised node still signs
  any app's layer until someone notices and revokes it.
- **Signing inside schedd on the control plane.** schedd also runs on compute
  nodes and has a large, network-facing API. A dedicated process keeps the
  key away from both.
- **Re-hashing layers on the control plane.** See section 4.
