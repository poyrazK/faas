package e2etest

// twonode_sql.go — the two-node fixture's compute_nodes seed statement, kept
// in an UNTAGGED file on purpose.
//
// twonode.go is `//go:build e2e || metal`. Nothing in CI builds with either
// tag (`make e2e` passes no tags; only `make test-metal` sets metal, and that
// needs KVM + root), so for as long as this statement lived there it was
// never compiled, let alone executed, by an ordinary test run. Four wrong
// column names accumulated in it undetected:
//
//   - vcpus — the real column is vpcpus. Migration 00024 created it with the
//     letters transposed and nothing has renamed it since, so the entire
//     platform spells it vpcpus (cmd/vmmd/register.go, cmd/apid,
//     cmd/gregalectl, PgStore.UpsertComputeNodeFromVmmd).
//   - plan_host, overlay_ip, gateway_port — not columns of compute_nodes at
//     all. They appeared nowhere else in the repository and nothing ever read
//     them back.
//
// The first run on real hardware failed all six two-node tests with
// SQLSTATE 42703, one column at a time. Living here, the statement is
// compiled by every build and exercised by twonode_schema_test.go against a
// migrated schema in normal CI.
//
// Keep the column list aligned with PgStore.UpsertComputeNodeFromVmmd — that
// is the authoritative writer for this table.
const upsertComputeNodeSQL = `INSERT INTO compute_nodes
	(name, target_url, schedd_target_url, gateway_target_url, lifecycle,
	 mem_mb, max_concurrency, admission_ceiling_mb, vpcpus, vcpu_budget)
	VALUES ($1, $2, $3, $4, 'active'::compute_node_lifecycle,
		8192, 16, 256, 4, 160)
	ON CONFLICT (name) DO UPDATE SET
		schedd_target_url = EXCLUDED.schedd_target_url,
		gateway_target_url = EXCLUDED.gateway_target_url
	RETURNING id`
