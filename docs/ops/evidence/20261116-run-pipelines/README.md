# Run pipeline qualification — 2026-11-16

Functional tests and all five Linux lint groups pass on
`gregale-internal-test-1` in `gregale-prod`. No production deployment or feature
activation has been performed. The pipeline gate remains off by default.

The lane uses a private source copy based on the previously qualified Run controls
manifest `908b512feece17b5dd87810a6496e6cd57820aea9c37851b3d15582e312c0522`.
The reviewed delta archive is
`44df733d104bd577acaa66bae648f7fbd7dc2b7eaf6197de13a76a71e95deb4d`, followed by
request-copy/test correction archive
`32f32868f38b0779737a099e1a0c25a9e15259f3dbbf305478ab7e93b251dc52`.
The final policy-copy/concurrency correction archive is
`0d2641863c9a1c8af0d3e5da4ab44d96d3692c670622ba95e7ac6003f906b7e6`.
All 55 files in [the source manifest](qualified-source-manifest.json) were checked
on the node before execution. Documentation and evidence were updated afterward;
implementation, API contracts, generated SQL, SDK Go code and tests match the
qualified source.

The checks cover pinned task/batch compilation; bounded infrastructure retries;
parallel branches and inline/stored artifact handoffs across continuation claims;
cancellation and expiry without a decryption identity; duplicate-attempt races;
stale-lease and cross-family cancellation fences; atomic customer/task budgets;
API retry, restart, deadline clipping and rollback replay; the seven existing
managed DAG scenarios; CLI workflow regression cases; OpenAPI and SDK coverage;
immutable caller/policy snapshots; batches serialized by task concurrency budgets;
and focused Go, Node and Python SDK contracts. See [the report](report.json) for
status and retained logs for commands and results.

PostgreSQL tests used private temporary databases through the existing local
`postgres` account. No roles, credentials or privilege grants were provisioned.
Their template/clone databases were cleaned up. A completed duplicate state-test
binary was losslessly archived, checksum-verified and recorded in
`state-binary-archive.json`; these checks did not evict Go cache entries.

This is control-plane and transaction qualification. It changes neither VM nor
guest lifecycle protocols. The GCP node supplies functional Linux evidence, not
the dedicated native x86_64 KVM acceptance required for production VM claims.

The repository-wide ADR uniqueness check still reports two pre-existing duplicate
prefixes: `20261104120000` and `20261111130000`. Those four files are unchanged;
the new pipeline decision has a unique prefix. All other completed scoped checks
are recorded separately.
