# FaasWorkloadIdentityNotConfigured

## Symptom

`vmmd_workload_identity_tokens_total{result="not_configured"}` increased: apps on
this compute node asked the guest token endpoint for a workload-identity token,
and vmmd has no signing key. Those apps cannot lease queue messages with the SDK
consumers, read Flags, or use cloud federation (ADR-1141).

## Check

```promql
sum by (instance, result) (increase(vmmd_workload_identity_tokens_total[15m]))
```

On the node, `systemctl cat faas-vmmd` should show
`99-faas-workload-identity.conf` with `FAAS_WORKLOAD_IDENTITY_KEY_PATH` and
`FAAS_WORKLOAD_IDENTITY_KEY_ID`. A missing drop-in means the key file
`/etc/faas/secrets/workload-identity.key` is absent. A present drop-in with
`workload identity signer unavailable` in the vmmd log means the key does not
parse or is shorter than 2048 bits.

## Recover

1. If the cluster has no key yet, create one on the operator workstation:
   `gregalectl workload-identity init --out <dir>`. Keep `<dir>` with the
   other operator secrets; it holds the private key.
2. Set `faas_workload_identity_source_dir: <dir>` for the inventory and run
   `bootstrap-control-plane` first (installs the JWKS apid trusts), then
   `bootstrap-compute` (installs the key and restarts vmmd).
3. Verify `result="minted"` increases and the alert clears. Every compute node
   must use the same key: apid and cloud providers trust one JWKS.

To rotate, run `gregalectl workload-identity rotate --out <dir>` and roll out
in the same order. The JWKS keeps the previous public key so tokens minted
before the rotation verify until they expire (5 minutes by default).
