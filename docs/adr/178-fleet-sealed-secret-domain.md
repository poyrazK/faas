# ADR-178: Dedicated fleet sealed-secret domain

- **Status:** accepted
- **Issue:** #2437

## Decision

Gregale uses `/etc/faas/secrets/fleet.age` for ciphertext that can be consumed on any control-plane or compute node. Its public half is `fleet.age.pub`. Every node receives the same fleet identity through the operator-authorized join artifact set over the pinned SSH transport.

`/etc/faas/secrets/host.age` remains independently generated on each host. It is retained in daemon credential sets during migration so old ciphertext can be opened, but new customer secrets and the cluster signing private key are sealed only to the fleet recipient. Copying or replacing host identities during node join is forbidden.

`gregalectl fleet-seal migrate` re-seals the cluster signing singleton and `app_secrets` rows using in-memory plaintext and compare-and-swap encrypted writes. `fleet_seal_domain_probe` contains a non-secret envelope created by the same `secretbox.SealOne` path as customer secrets. Node join verifies that probe, validates the cluster public/private key and `kid`, and completes a JWT mint/verify round trip before service activation or capacity admission.

The join input requires both `fleet.age` and its matching public recipient. An absent, mismatched, or unsealable identity fails the join while the database node remains drained. The node-exporter proof metric and Prometheus alerts detect a missing proof or divergent cluster `kid` after deployment.

## Consequences

Fleet identity compromise exposes fleet-sealed customer material, so the identity must be handled like a cluster root secret and rotated with a complete migration. Host identity compromise remains bounded to legacy ciphertext and host-local uses. Adding a node expands authorized unseal access by staging the fleet identity; it does not rewrite existing ciphertext or expose plaintext.
