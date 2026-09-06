# FaasUpstreamClassifierFailure

The apid env-classifier failed after persisting an environment row. The
customer request remains successful, but the derived `data_upstreams` hint may
be missing and connection-aware placement can fall back to its legacy path.

Check `apid_data_upstream_classifier_failures_total` grouped by `reason` and
inspect the matching `data_upstream.classifier_failed` audit events. A
`salt_missing` reason means the apid host cannot read the host-hash salt;
restore the configured salt and retry the env mutation. For
`port_out_of_range` or `unknown_kind`, inspect the classifier input and the
closed-vocabulary mapping. For `internal_error`, check apid and Postgres logs.

After remediation, repeat the env mutation or run the upstream backfill so the
derived hint is recreated, then confirm the counter rate returns to zero.
