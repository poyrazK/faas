# Checked project release activation

Prepare `release.json` with a retention TTL and one exact deployment UUID for
**every** project workload:

```json
{"ttl_seconds":1800,"deployments":{"shop-api":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","shop-billing":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}}
```

For a service binding, verify the caller against the target selected by that
map. This performs the platform HTTPS HEAD route check, without waking the
service or invoking its application handler:

```sh
gregale bindings verify shop-api shop-billing --deployment aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa --target-deployment bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb
gregale projects environments release-sets check shop production --file release.json --expected-active none
gregale projects environments release-sets publish shop production --file release.json --expected-active none
```

Use the current active release UUID instead of `none` when replacing a graph.
The check reports the exact membership, digest and per-deployment blockers and
creates no probes. Publication independently rechecks evidence, applies stored
policies and atomically compares and replaces the active graph. One failed or
expired member blocks the entire switch. A successful receipt confirms exactly
the requested graph; previous releases retain their compatibility TTL.
`--json` includes the structured check or activation receipt. A blocked check
exits nonzero. Environment promotion and rollback remain separate workflows.
