# ADR-184 · Operator-managed reserved public IP inventory

- **Status:** accepted control-plane follow-up to ADR-171
- **Date:** 2026-09-19
- **Decision:** keep a durable, provider-neutral inventory of operator-provisioned public addresses and atomically claim an available inventory row into one account-scoped reserved-IP lease.
- **Why:** a multi-node platform needs a platform-owned address pool before a provider connector can safely perform route movement. Allowing tenants to submit arbitrary addresses would bypass ownership and provider reachability checks.
- **Consequences:** inventory has explicit `available`, `claimed`, and `retired` states; provider/source references and addresses are unique; claims and releases are transactional and fail closed when a lease is assigned to a workload. Physical route movement, provider adapters, customer APIs, quotas, and billing remain separate follow-up work.
- **Rejected alternatives:** reusing the legacy single-node static-egress table would couple the new multi-node contract to an account/customer-IP row; accepting arbitrary customer IPs would make ownership and reachability unverifiable.
