# Data API runtime connection observations

Apply the normal control-plane migrations and deploy apid with cluster workload
trust (`FAAS_WORKLOAD_JWKS_PATH`, optional `FAAS_WORKLOAD_ISSUER`) configured for
the signing key used by vmmd. The new workload-only report endpoint is
`POST /v1/runtime/data-api/connection-observations`. Owner API keys cannot publish
reports. Nothing is started automatically on existing deployments.

Rebuild the generated Data API runtime to include its reporter. For a ready
managed database and ready Data API binding using `DATABASE_URL`, obtain the
binding UUID, current database and credential generations from the operator
catalog. Compute the database identity from the exact frozen dataset:

```sql
SELECT encode(sha256(convert_to(backend_fingerprint || ':' || data_resource_id,
                              'UTF8')), 'hex') AS database_identity,
       desired_generation
FROM managed_postgres_databases
WHERE id = :'database_id'::uuid AND account_id = :'account_id'::uuid
  AND state = 'ready' AND data_resource_id <> ''
  AND desired_generation = observed_generation;
```

Set `DATA_API_CONNECTION_OBSERVATION` in the app configuration to a JSON object
with these five fields, using the actual catalog values:

```json
{
  "api_url": "https://control.example.com",
  "binding_id": "11111111-1111-4111-8111-111111111111",
  "database_identity": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "database_generation": 1,
  "credential_generation": 1
}
```

The UUID and digest above are placeholders. The API URL must be an HTTPS origin
without credentials, path, query or fragment. Guest-init supplies
`FAAS_WORKLOAD_IDENTITY_ENDPOINT`; do not put a workload token or owner API key
in the image or app configuration. Local development without a guest identity
endpoint should omit `DATA_API_CONNECTION_OBSERVATION`.

Deploy through the ordinary app workflow. The reporter verifies PostgREST 14.3,
then sends bounded reports after startup and every 15 seconds after completion.
The node must have a fresh heartbeat and the reporting instance must be running.
Initial clone-owned databases and non-default environment keys are unsupported.
Refresh the immutable observation configuration and roll the deployment when
database identity/generation or binding credential generation changes. An old
snapshot intentionally cannot discover and adopt the new generation itself.

Read the existing connection-budget endpoint or `gregale data-api health APP`.
`demand.runtime_observations` distinguishes observed configured resident pools,
unobserved residents and parked instances. `observed` means the resident subset
is covered by workload reports; it does not verify future wakes, actual session
usage, image integrity, Auth or external consumers. `partial` reports incomplete
resident/parked coverage. No reports means `unknown`, never zero connection use.

Reports expire within 30 seconds, possibly earlier with token expiry. Invalid
identity, expired credentials, stale node heartbeat, generation mismatch or
endpoint/network failure cannot extend evidence. A successful `unknown` report
clears earlier success immediately. Clock skew into the future is rejected;
check guest clock synchronization if reports remain absent. Error responses and
runtime logs do not contain roles, tokens, connection strings or raw upstream
errors. To disable the feature, remove the opt-in configuration and redeploy;
remaining evidence expires naturally.

Budget reads never query tenant SQL, start a runtime, or reserve connections.
Capacity still requires the separate [qualified capacity observer](DataAPIConnectionCapacity.md).
Full model/consumer coverage and aggregate enforcement remain pending.
