# Service-binding SDK contract

Go, Node and Python consume the same `cases.json` for app creation, updates and
reads. Cases cover policy and transport enums, declared targets, caller grants,
HTTP scopes, reliability settings, fingerprint preconditions, and nested manifest
transport. PATCH cases distinguish omission (retain), `null` (reset), and empty
caller lists/scopes (deny all). Empty reliability policies clear overrides.

`schema.json` captures the structural service-binding fields from
`CreateAppRequest`, `UpdateAppRequest`, `AppResponse` and `AppManifest`, together
with their referenced types. The checker discovers fields by name, follows
references, and pins required fields, nullability, enums and constraints. Prose
and unrelated app fields are excluded. Every captured field and named enum value
must appear in the shared cases.

Run from the repository root (Python needs `ruamel.yaml`, also used by SDK generation):

```sh
python scripts/ci/check-service-binding-sdk-contract.py
python scripts/ci/test_service_binding_sdk_contract.py
(cd sdk/go && go test ./... -run '^TestServiceBindingContract' -count=1)
(cd sdk/node && npm run test:build && node --test dist-test/test/service-binding-contract.test.js)
(cd sdk/python && python -m pytest tests/test_service_binding_contract.py -q)
```

When OpenAPI changes, review the SDK types and add cases for the new behavior,
then explicitly refresh the schema with
`python scripts/ci/check-service-binding-sdk-contract.py --write-schema` and run
all three SDK tests. The PR CI contract job checks the schema and all three SDKs;
each SDK's normal unit job also runs its fixture tests. Declared-field checks catch missing typed fields even
when a client preserves unknown JSON properties.

The schema snapshot detects API drift; it does not add runtime request validation
to SDKs. Fixtures use nonzero reliability values and explicit `true` so Go's
`omitempty` does not normalize them away. Unrestricted read policies are omitted;
explicit empty caller policies remain present. PATCH transport/policy `null` is
a no-op and is outside the exact wire cases (Go uses nil pointers to omit these
fields). Tests use in-memory HTTP transports and require no credentials or native
VMs.
