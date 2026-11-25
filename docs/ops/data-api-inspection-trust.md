# Data API inspection trust

The platform task for hosted Data API write inspection requires a dedicated
PostgreSQL trust bundle on the VM host. Configure
`data_api_inspection_ca_path` in `/etc/faas/vmmd.toml` with an operator-owned PEM file
containing the public CA certificates that validate the native PostgreSQL
proxy's server certificate. The file is limited to 32 KiB and must contain
only CA certificates. An empty setting disables this inspection; an invalid
configured file prevents vmmd startup. Restart vmmd to load a changed bundle.

The host attaches the validated bundle to the versioned task request over the
host-to-guest channel. The guest does not use the application image's system
trust store, a `PGSSLROOTCERT` value, or trust supplied in a customer task
request. A missing bundle or a TLS verification failure makes the inspection
unavailable. The bundle contains public certificates, never private keys.

For a native managed PostgreSQL backend, set its top-level
`data_api_inspection_trust_sha256` field to the canonical fingerprint of the
same CA set. Generate the value from the checked-in source on a trusted host:

```sh
go run ./cmd/data-api-inspection-trust /etc/faas/pgproxy/inspection-ca.crt
```

The fingerprint is over the CA certificate set, so PEM formatting, certificate
order, and duplicate certificates do not change it. The backend field is
independent of placement and does not alter existing databases' placement
fingerprints. A missing or mismatched provider fingerprint must leave identity
unverified. When rotating CAs, coordinate the backend setting and host bundles;
mixed trust sets cannot establish verified identity. A fingerprint is public,
but provisioning it is an operator trust decision.

On successful, untruncated inspection tasks, the scheduler records the
protocol version and a SHA-256 digest of the retained output atomically with
task completion. The current inspection protocol is version 6; an older guest
cannot produce its trust fingerprint, and version 5 evidence is insufficient.
The control plane also pins a server-generated nonce, the selected deployment,
binding, credential generation and runtime configuration. It checks those
pins again after the provider resolves its current target and CA expectation.
A verified verdict applies to the observed database target and login at that
time; it does not establish row authorization or future behavior. Evidence
older than five minutes is reported as `unknown`. The control
plane returns `unknown` for stale, incomplete, incompatible or changed
evidence, and `mismatch` when trusted execution observes a different target.
Inspection output is available through the dedicated inspection endpoint,
which requires managed PostgreSQL read access; generic app-task responses do
not expose its stdout or stderr.

Roll out the database migrations before control-plane or scheduler binaries,
then deploy compatible vmmd and guest-init versions. During rolling upgrades,
unsupported components remain `unknown`. Unsuccessful inspections and hosts
without a configured trust bundle do not produce verified evidence.
Rolling back the protocol-version migration is refused while version 6
evidence remains, rather than silently deleting those records.

For a disposable local integration check, build the CLI and run the opt-in
test with PostgreSQL binaries available:

```sh
test_dir=$(mktemp -d)
go build -o "$test_dir/gregale" ./cmd/gregale
GREGALE_DATA_API_HOSTED_ACCEPTANCE=1 \
  GREGALE_TEST_POSTGRES_BIN="$(pg_config --bindir)" \
  GREGALE_INSPECTION_CLI_BIN="$test_dir/gregale" \
  go test ./pkg/apid/apidserver -run '^TestDataAPIInspectionDisposableCLIIntegration$' -count=1
rm -rf "$test_dir"
```

The test creates and removes its own PostgreSQL cluster and uses the real CLI,
control plane and scheduler. It simulates the vmmd/guest boundary, so it does
not qualify Firecracker execution or runtime credential delivery. The Data API
acceptance workflow runs this test and the scheduler provenance checks in its
`inspection-integration` job; the staging canary is a separate check.
